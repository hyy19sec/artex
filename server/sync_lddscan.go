package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/xuri/excelize/v2"
)

// 资产同步（lddscan xlsx 导入）。
//
// lddscan 是一个资产测绘工具，导出多 sheet 的 xlsx。本适配器解析其中的
// Target / DNS Info / Http Data 三个 sheet，映射为 ARTEX 的公司 + 资产模型。
//
// 与 sync_scopesentry.go 平行：全部走 AssetStore.Upsert*（不写裸 SQL），
// 以复用其副作用（root_domain / ip 联动、公司归属、去重）。
//
// 注意：xlsx 的 content（响应正文）会被 Excel 单元格 32767 字符截断——但
// ARTEX 资产表本就不存正文（只有 content_length），且 content_length 取的是
// 真实的 length 列，故截断对导入无影响。

const (
	lddscanMaxUpload   = 64 << 20 // 单文件上限
	lddscanMaxRows     = 500000   // 单 sheet 行数保护上限
	lddscanCtxEvery    = 500      // 每 N 行检查一次 ctx 取消
	lddscanHTTPTimeout = 15 * time.Minute
)

// lddscanResult mirrors the ssSync response shape so the frontend can reuse it.
type lddscanResult struct {
	Synced    map[string]int `json:"synced"`
	Companies []string       `json:"companies"`
	Warnings  []string       `json:"warnings"`
	Errors    []string       `json:"errors"`
}

func (r *lddscanResult) warn(format string, a ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, a...))
}
func (r *lddscanResult) fail(format string, a ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, a...))
}

// ---------- POST /api/sync/lddscan/import ----------

func (s *Server) syncLddscanImport(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	cs := s.companyStore()
	if as == nil || cs == nil {
		writeErr(w, 503, "database unavailable")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, lddscanMaxUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "解析上传失败或超出大小限制: "+err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少上传文件(表单字段 file)")
		return
	}
	defer file.Close()

	ctx, cancel := context.WithTimeout(r.Context(), lddscanHTTPTimeout)
	defer cancel()

	f, err := excelize.OpenReader(file)
	if err != nil {
		writeErr(w, 400, "打开 xlsx 失败: "+err.Error())
		return
	}
	defer f.Close()

	name := strings.TrimSpace(hdr.Filename)
	if name == "" {
		name = "lddscan"
	}
	reason := "lddscan:" + name

	res := &lddscanResult{
		Synced:    map[string]int{"root_domain": 0, "subdomain": 0, "ip": 0, "service": 0},
		Companies: []string{},
		Warnings:  []string{},
		Errors:    []string{},
	}

	if err := s.lddscanTargets(ctx, f, as, cs, reason, res); err != nil {
		res.fail("Target: %v", err)
	}
	if err := s.lddscanDNS(ctx, f, as, res); err != nil {
		res.fail("DNS Info: %v", err)
	}
	if err := s.lddscanHTTP(ctx, f, as, res); err != nil {
		res.fail("Http Data: %v", err)
	}
	if len(res.Companies) > 0 {
		if err := cs.RecomputeAttribution(); err != nil {
			res.warn("公司归属回填失败: %v", err)
		}
	}

	writeJSON(w, 200, res)
}

// ---------- Target sheet ----------

// lddscanTargets reads the Target sheet → root_domain assets, and (when a
// `company` column is present and non-empty) creates companies + company scope
// grouped by company name.
func (s *Server) lddscanTargets(ctx context.Context, f *excelize.File, as *db.AssetStore, cs *db.CompanyStore, reason string, res *lddscanResult) error {
	rows, cols, err := openSheet(f, "Target")
	if err != nil || rows == nil {
		return err
	}
	defer rows.Close()

	byCompany := map[string][]string{}
	var order []string
	n := 0
	for rows.Next() {
		if n++; n%lddscanCtxEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		row, err := rows.Columns()
		if err != nil {
			continue
		}
		target := cell(row, cols, "target")
		if target == "" {
			continue
		}
		if _, err := as.UpsertRootDomain(db.UpsertRootDomainReq{Domain: target}); err != nil {
			res.fail("root_domain %s: %v", target, err)
			continue
		}
		res.Synced["root_domain"]++
		if comp := cell(row, cols, "company"); comp != "" {
			if _, ok := byCompany[comp]; !ok {
				order = append(order, comp)
			}
			byCompany[comp] = append(byCompany[comp], target)
		}
	}
	if err := rows.Error(); err != nil {
		return err
	}

	for _, comp := range order {
		cid, err := cs.UpsertByName(comp)
		if err != nil {
			res.warn("公司 %s 创建失败: %v", comp, err)
			continue
		}
		_, _, _, errs := cs.AddScope(cid, byCompany[comp], reason)
		res.Companies = append(res.Companies, comp)
		res.Warnings = append(res.Warnings, errs...)
	}
	return nil
}

// ---------- DNS Info sheet ----------

// lddscanDNS reads the DNS Info sheet → subdomain assets (A / CNAME records).
// UpsertSubdomain already upserts the bound IP rows for A/AAAA records, so the
// IP count is reported from the parsed A values rather than a second call.
func (s *Server) lddscanDNS(ctx context.Context, f *excelize.File, as *db.AssetStore, res *lddscanResult) error {
	rows, cols, err := openSheet(f, "DNS Info")
	if err != nil || rows == nil {
		return err
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		if n++; n%lddscanCtxEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		row, err := rows.Columns()
		if err != nil {
			continue
		}
		domain := cell(row, cols, "domain")
		if domain == "" {
			continue
		}
		if ips := parsePyList(cell(row, cols, "a")); len(ips) > 0 {
			if _, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: domain, RecordType: "A", RecordValue: ips}); err != nil {
				res.fail("subdomain %s: %v", domain, err)
			} else {
				res.Synced["subdomain"]++
				for _, ip := range ips {
					if net.ParseIP(ip) != nil {
						res.Synced["ip"]++
					}
				}
			}
		}
		if cn := cell(row, cols, "cname"); cn != "" {
			if _, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: domain, RecordType: "CNAME", RecordValue: []string{cn}}); err != nil {
				res.fail("subdomain %s(CNAME): %v", domain, err)
			} else {
				res.Synced["subdomain"]++
			}
		}
	}
	return rows.Error()
}

// ---------- Http Data sheet ----------

// lddscanHTTP reads the Http Data (or Url Dedup) sheet → HTTP service assets.
func (s *Server) lddscanHTTP(ctx context.Context, f *excelize.File, as *db.AssetStore, res *lddscanResult) error {
	sheet := resolveSheet(f, "Http Data", "Url Dedup", "Hunter", "Fofa")
	if sheet == "" {
		res.warn("未找到 Http Data / Url Dedup sheet，跳过 HTTP 服务导入")
		return nil
	}
	rows, cols, err := openSheet(f, sheet)
	if err != nil || rows == nil {
		return err
	}
	defer rows.Close()

	// column aliases across the different sheets
	urlCol := firstCol(cols, "url", "link", "origin_url")
	titleCol := firstCol(cols, "title", "web_title")
	ipCol := firstCol(cols, "ip")
	statusCol := firstCol(cols, "status_code")
	lenCol := firstCol(cols, "length", "chars_len")
	favCol := firstCol(cols, "favhash")
	fingerCol := firstCol(cols, "finger")
	// crawl_flag 原样映射为 has_js：lddscan 的 1/True 表示该页抓到了 JS 引用
	// （asset_refs 非空），即存在前端接口、值得优先测试；0/False 表示没有。
	crawlCol := firstCol(cols, "crawl_flag")
	// cert_ERROR=误报：lddscan 对 IP 主机用占位 SNI（如 www.notexist.com）探测，
	// 证书错误被判为误报。按用户要求这类行不导入。
	certErrCol := firstCol(cols, "cert_error")

	n := 0
	skippedCertErr := 0
	for rows.Next() {
		if n++; n%lddscanCtxEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		row, err := rows.Columns()
		if err != nil {
			continue
		}
		url := cell(row, cols, urlCol)
		if url == "" {
			continue
		}
		if isCertErrorMisreport(cell(row, cols, certErrCol)) {
			skippedCertErr++
			continue
		}
		req := db.UpsertHTTPServiceReq{
			URL:          url,
			IP:           validIP(cell(row, cols, ipCol)),
			PageTitle:    cell(row, cols, titleCol),
			Technologies: parsePyList(cell(row, cols, fingerCol)),
		}
		if sc := atoi(cell(row, cols, statusCol)); sc > 0 {
			req.StatusCode = &sc
		}
		if cl := atoi64(cell(row, cols, lenCol)); cl > 0 {
			req.ContentLength = &cl
		}
		if fav := firstOf(parsePyList(cell(row, cols, favCol))); fav != "" {
			req.FaviconMMH3 = fav
		}
		// 空值（失败行）保持 nil，表示"未知"，不要误写成 false。
		switch strings.ToLower(cell(row, cols, crawlCol)) {
		case "1", "true", "yes":
			hasJS := true
			req.HasJS = &hasJS
		case "0", "false", "no":
			hasJS := false
			req.HasJS = &hasJS
		}
		if _, err := as.UpsertHTTPService(req); err != nil {
			res.fail("service %s: %v", url, err)
			continue
		}
		res.Synced["service"]++
	}
	if err := rows.Error(); err != nil {
		return err
	}
	if skippedCertErr > 0 {
		res.warn("已跳过 cert_ERROR=误报 的 HTTP 记录: %d 条", skippedCertErr)
	}
	return nil
}

// isCertErrorMisreport reports whether lddscan marked the TLS certificate error
// as a probe artifact ("误报"). The row's HTTP request itself may have succeeded,
// but these are IP:port services probed with a placeholder SNI, so the user
// chose not to import them.
func isCertErrorMisreport(v string) bool {
	v = strings.TrimSpace(v)
	return v == "误报" || v == "誤報"
}

// ---------- sheet / cell helpers ----------

// openSheet opens a named sheet and returns its streaming row iterator plus a
// lowercased header→column-index map. An absent/empty sheet → (nil, nil, nil).
func openSheet(f *excelize.File, name string) (*excelize.Rows, map[string]int, error) {
	if !sheetExists(f, name) {
		return nil, nil, nil
	}
	rows, err := f.Rows(name)
	if err != nil {
		return nil, nil, err
	}
	if !rows.Next() {
		_ = rows.Close()
		return nil, nil, nil
	}
	headers, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	cols := make(map[string]int, len(headers))
	for i, h := range headers {
		if k := strings.ToLower(strings.TrimSpace(h)); k != "" {
			cols[k] = i
		}
	}
	return rows, cols, nil
}

// sheetExists reports whether a sheet with this exact name exists.
func sheetExists(f *excelize.File, name string) bool {
	for _, s := range f.GetSheetList() {
		if s == name {
			return true
		}
	}
	return false
}

// resolveSheet returns the first of names present in the workbook (exact match),
// or "" if none exist.
func resolveSheet(f *excelize.File, names ...string) string {
	for _, n := range names {
		if sheetExists(f, n) {
			return n
		}
	}
	return ""
}

// cell returns the trimmed value of a column (by lowercased header) or "".
func cell(row []string, cols map[string]int, col string) string {
	i, ok := cols[strings.ToLower(col)]
	if !ok || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// firstCol returns the first header key present in cols, or "".
func firstCol(cols map[string]int, names ...string) string {
	for _, n := range names {
		if _, ok := cols[strings.ToLower(n)]; ok {
			return n
		}
	}
	return ""
}

// ---------- value helpers ----------

// parsePyList parses a python-repr list like "['a', 'b']" into ["a","b"].
// Non-list input is treated as a single value (quotes stripped). "[]" / "None"
// / "" → nil.
func parsePyList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" || s == "None" || s == "null" {
		return nil
	}
	if !(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) {
		if v := strings.Trim(s, `'"`); v != "" {
			return []string{v}
		}
		return nil
	}
	inner := s[1 : len(s)-1]
	var out []string
	var buf strings.Builder
	var quote rune
	for _, r := range inner {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				buf.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ',':
			if v := strings.TrimSpace(buf.String()); v != "" {
				out = append(out, v)
			}
			buf.Reset()
		default:
			buf.WriteRune(r)
		}
	}
	if v := strings.TrimSpace(buf.String()); v != "" {
		out = append(out, v)
	}
	return out
}

// validIP returns s if it is a parseable IPv4/IPv6 address, else "" (assets.ip
// rejects hostnames, and UpsertHTTPService fails the whole row on a bad value).
func validIP(s string) string {
	if net.ParseIP(strings.TrimSpace(s)) != nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func firstOf(ss []string) string {
	if len(ss) > 0 {
		return ss[0]
	}
	return ""
}

// atoi parses an int, tolerating floats ("443.0") and junk (→ 0).
func atoi(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	return 0
}

func atoi64(s string) int64 {
	return int64(atoi(s))
}
