package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrRatingTemplateNotFound marks a missing template id.
var ErrRatingTemplateNotFound = errors.New("rating template not found")

// RatingTemplate is a reusable rating standard (互联网 SRC / 银行 SRC / 攻防 …).
// Tasks snapshot its clauses into task_constraints(scope='rating') at creation or
// when the operator applies it; the snapshot row ids become rating_clause_id.
type RatingTemplate struct {
	ID          int64                  `json:"id"`
	NKey        string                 `json:"-"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Builtin     bool                   `json:"builtin"`
	Clauses     []RatingTemplateClause `json:"clauses,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// RatingTemplateClause is one clause in a rating template.
type RatingTemplateClause struct {
	ID         int64  `json:"id"`
	TemplateID int64  `json:"template_id"`
	Code       string `json:"code"` // 严重-1 / 高危-2 / 忽略-1 ...
	Kind       string `json:"kind"` // allow(计入) | deny(不收取/忽略)
	Text       string `json:"text"`
	SortOrder  int    `json:"sort_order"`
}

// RatingTemplateInput is the create/update payload after validation.
type RatingTemplateInput struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Clauses     []RatingTemplateClauseInput `json:"clauses"`
}

// RatingTemplateClauseInput is one clause in a template input.
type RatingTemplateClauseInput struct {
	Code      string `json:"code"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	SortOrder int    `json:"sort_order"`
}

func normalizeTemplateClause(in RatingTemplateClauseInput) (RatingTemplateClauseInput, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.Text = strings.TrimSpace(in.Text)
	if in.Kind == "" {
		in.Kind = "allow"
	}
	if in.Kind != "allow" && in.Kind != "deny" {
		return in, fmt.Errorf("条款 kind 必须是 allow 或 deny")
	}
	if in.Text == "" {
		return in, fmt.Errorf("条款 text 不能为空")
	}
	return in, nil
}

func normalizeRatingTemplateInput(in RatingTemplateInput) (RatingTemplateInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" {
		return in, fmt.Errorf("模板名称不能为空")
	}
	if len(in.Clauses) == 0 {
		return in, fmt.Errorf("模板至少需要一条条款")
	}
	clauses := make([]RatingTemplateClauseInput, 0, len(in.Clauses))
	for i, c := range in.Clauses {
		nc, err := normalizeTemplateClause(c)
		if err != nil {
			return in, fmt.Errorf("第 %d 条条款无效: %w", i+1, err)
		}
		if nc.SortOrder == 0 {
			nc.SortOrder = i
		}
		clauses = append(clauses, nc)
	}
	in.Clauses = clauses
	return in, nil
}

// ListRatingTemplates returns every template with its clauses, builtins first.
func (d *DB) ListRatingTemplates() ([]*RatingTemplate, error) {
	rows, err := d.Query(`SELECT id, nkey, name, COALESCE(description,''), builtin, created_at, updated_at
FROM rating_templates ORDER BY builtin DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RatingTemplate
	byID := map[int64]*RatingTemplate{}
	for rows.Next() {
		t := &RatingTemplate{}
		if err := rows.Scan(&t.ID, &t.NKey, &t.Name, &t.Description, &t.Builtin, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Clauses = []RatingTemplateClause{}
		byID[t.ID] = t
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	cr, err := d.Query(`SELECT id, template_id, COALESCE(code,''), kind, text, sort_order
FROM rating_template_clauses ORDER BY template_id, sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer cr.Close()
	for cr.Next() {
		var c RatingTemplateClause
		if err := cr.Scan(&c.ID, &c.TemplateID, &c.Code, &c.Kind, &c.Text, &c.SortOrder); err != nil {
			return nil, err
		}
		if t := byID[c.TemplateID]; t != nil {
			t.Clauses = append(t.Clauses, c)
		}
	}
	return out, cr.Err()
}

// GetRatingTemplate returns one template with its clauses.
func (d *DB) GetRatingTemplate(id int64) (*RatingTemplate, error) {
	t := &RatingTemplate{}
	err := d.QueryRow(`SELECT id, nkey, name, COALESCE(description,''), builtin, created_at, updated_at
FROM rating_templates WHERE id=$1`, id).Scan(&t.ID, &t.NKey, &t.Name, &t.Description, &t.Builtin, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrRatingTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := d.Query(`SELECT id, template_id, COALESCE(code,''), kind, text, sort_order
FROM rating_template_clauses WHERE template_id=$1 ORDER BY sort_order, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	t.Clauses = []RatingTemplateClause{}
	for rows.Next() {
		var c RatingTemplateClause
		if err := rows.Scan(&c.ID, &c.TemplateID, &c.Code, &c.Kind, &c.Text, &c.SortOrder); err != nil {
			return nil, err
		}
		t.Clauses = append(t.Clauses, c)
	}
	return t, rows.Err()
}

// CreateRatingTemplate inserts a template and its clauses in one transaction.
func (d *DB) CreateRatingTemplate(in RatingTemplateInput) (*RatingTemplate, error) {
	in, err := normalizeRatingTemplateInput(in)
	if err != nil {
		return nil, err
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var id int64
	if err := tx.QueryRow(`INSERT INTO rating_templates(nkey,name,description,builtin)
VALUES ('custom-' || nextval('rating_templates_id_seq'), $1, $2, false) RETURNING id`,
		in.Name, in.Description).Scan(&id); err != nil {
		return nil, err
	}
	if err := insertTemplateClausesTx(tx, id, in.Clauses); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetRatingTemplate(id)
}

// UpdateRatingTemplate replaces a template's fields and clauses. Builtin templates
// are editable too; the one-time seed flag means edits/deletions are not undone.
func (d *DB) UpdateRatingTemplate(id int64, in RatingTemplateInput) (*RatingTemplate, error) {
	in, err := normalizeRatingTemplateInput(in)
	if err != nil {
		return nil, err
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.Exec(`UPDATE rating_templates SET name=$2, description=$3 WHERE id=$1`, id, in.Name, in.Description)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrRatingTemplateNotFound
	}
	if _, err := tx.Exec(`DELETE FROM rating_template_clauses WHERE template_id=$1`, id); err != nil {
		return nil, err
	}
	if err := insertTemplateClausesTx(tx, id, in.Clauses); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetRatingTemplate(id)
}

// DeleteRatingTemplate removes a template and its clauses. Tasks keep their
// snapshot constraints; tasks.rating_template_id becomes NULL.
func (d *DB) DeleteRatingTemplate(id int64) (bool, error) {
	res, err := d.Exec(`DELETE FROM rating_templates WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func insertTemplateClausesTx(tx *sql.Tx, templateID int64, clauses []RatingTemplateClauseInput) error {
	for _, c := range clauses {
		if _, err := tx.Exec(`INSERT INTO rating_template_clauses(template_id, code, kind, text, sort_order)
VALUES ($1,$2,$3,$4,$5)`, templateID, c.Code, c.Kind, c.Text, c.SortOrder); err != nil {
			return err
		}
	}
	return nil
}

// ratingClauseText embeds the code into the snapshot text so the constraint block
// and findings can display it without a separate task_constraints.code column.
func ratingClauseText(code, text string) string {
	code = strings.TrimSpace(code)
	text = strings.TrimSpace(text)
	if code == "" {
		return text
	}
	return "【" + code + "】" + text
}

func loadTemplateClauseInputsTx(tx *sql.Tx, templateID int64) ([]RatingTemplateClauseInput, error) {
	rows, err := tx.Query(`SELECT COALESCE(code,''), kind, text, sort_order
FROM rating_template_clauses WHERE template_id=$1 ORDER BY sort_order, id`, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RatingTemplateClauseInput
	for rows.Next() {
		var c RatingTemplateClauseInput
		if err := rows.Scan(&c.Code, &c.Kind, &c.Text, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// snapshotRatingTemplateTx copies a template's clauses into task_constraints for a
// newly created task, inside the task-creation transaction.
func snapshotRatingTemplateTx(tx *sql.Tx, explorationID, templateID int64) error {
	if templateID <= 0 {
		return nil
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM rating_templates WHERE id=$1)`, templateID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrRatingTemplateNotFound
	}
	clauses, err := loadTemplateClauseInputsTx(tx, templateID)
	if err != nil {
		return err
	}
	origin := fmt.Sprintf("template:%d", templateID)
	for _, c := range clauses {
		if _, err := tx.Exec(`INSERT INTO task_constraints(exploration_id, kind, scope, text, origin)
VALUES ($1,$2,'rating',$3,$4)`, explorationID, c.Kind, ratingClauseText(c.Code, c.Text), origin); err != nil {
			return err
		}
	}
	return nil
}

// ApplyRatingTemplate copies a template into an existing task's active rating
// constraints. replace=true supersedes all currently active rating rows first;
// replace=false appends only clauses whose text is not already active.
// It also updates tasks.rating_template_id.
func (d *DB) ApplyRatingTemplate(explorationID, templateID int64, replace bool) (int, error) {
	clauses, err := loadTemplateClauseInputs(d, templateID)
	if err != nil {
		return 0, err
	}
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	if replace {
		if _, err := tx.Exec(`UPDATE task_constraints SET status='superseded', updated_at=now()
WHERE exploration_id=$1 AND scope='rating' AND status='active'`, explorationID); err != nil {
			return 0, err
		}
	}
	origin := fmt.Sprintf("template:%d", templateID)
	added := 0
	for _, c := range clauses {
		text := ratingClauseText(c.Code, c.Text)
		if !replace {
			var exists bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM task_constraints
WHERE exploration_id=$1 AND scope='rating' AND status='active' AND text=$2)`, explorationID, text).Scan(&exists); err != nil {
				return 0, err
			}
			if exists {
				continue
			}
		}
		if _, err := tx.Exec(`INSERT INTO task_constraints(exploration_id, kind, scope, text, origin)
VALUES ($1,$2,'rating',$3,$4)`, explorationID, c.Kind, text, origin); err != nil {
			return 0, err
		}
		added++
	}
	if _, err := tx.Exec(`UPDATE tasks SET rating_template_id=$2 WHERE exploration_id=$1 AND deleted_at IS NULL`, explorationID, templateID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

func loadTemplateClauseInputs(d *DB, templateID int64) ([]RatingTemplateClauseInput, error) {
	t, err := d.GetRatingTemplate(templateID)
	if err != nil {
		return nil, err
	}
	out := make([]RatingTemplateClauseInput, 0, len(t.Clauses))
	for _, c := range t.Clauses {
		out = append(out, RatingTemplateClauseInput{Code: c.Code, Kind: c.Kind, Text: c.Text, SortOrder: c.SortOrder})
	}
	return out, nil
}

// RatingCriteria is the read model for GET /api/tasks/{id}/rating-criteria and
// the get_rating_criteria tool.
type RatingCriteria struct {
	Template *RatingTemplate `json:"template,omitempty"`
	Clauses  []Constraint    `json:"clauses"`
}

// RatingCriteria returns the task's selected template (if any) and its active
// rating constraints (the snapshot).
func (s *ExplorationStore) RatingCriteria() (*RatingCriteria, error) {
	all, err := s.ListConstraints()
	if err != nil {
		return nil, err
	}
	criteria := &RatingCriteria{Clauses: []Constraint{}}
	for _, c := range all {
		if c.Scope == "rating" && c.Status != "superseded" {
			criteria.Clauses = append(criteria.Clauses, c)
		}
	}
	var templateID sql.NullInt64
	err = s.db.QueryRow(`SELECT rating_template_id FROM tasks WHERE exploration_id=$1 AND deleted_at IS NULL`, s.expID).Scan(&templateID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if templateID.Valid && templateID.Int64 > 0 {
		tpl, err := s.db.GetRatingTemplate(templateID.Int64)
		if err == nil {
			criteria.Template = tpl
		} else if !errors.Is(err, ErrRatingTemplateNotFound) {
			return nil, err
		}
	}
	return criteria, nil
}

// ActiveRatingConstraints returns the active scope=rating rows, used by
// report_finding to validate rating_clause_id.
func (s *ExplorationStore) ActiveRatingConstraints() ([]Constraint, error) {
	rows, err := s.db.Query(`SELECT id, kind, scope, text, COALESCE(origin,''), status, supersedes_id, created_at
FROM task_constraints WHERE exploration_id=$1 AND scope='rating' AND status='active' ORDER BY id`, s.expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Constraint
	for rows.Next() {
		var c Constraint
		var supersedes sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Kind, &c.Scope, &c.Text, &c.Origin, &c.Status, &supersedes, &c.CreatedAt); err != nil {
			return nil, err
		}
		if supersedes.Valid {
			v := supersedes.Int64
			c.SupersedesID = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SeedRatingTemplates seeds the three built-in templates once per database. A
// settings flag guards it so user edits/deletions are not undone on restart.
func (d *DB) SeedRatingTemplates() error {
	const flag = "rating_templates_seed_v1"
	if v, ok, err := d.GetSetting(flag); err == nil && ok && v == "true" {
		return nil
	}
	type seed struct {
		nkey, name, desc string
		clauses          []RatingTemplateClauseInput
	}
	seeds := []seed{
		{
			nkey: "internet-src", name: "互联网 SRC",
			desc: "互联网 SRC 通用示例模板，请按实际 SRC 标准调整。",
			clauses: []RatingTemplateClauseInput{
				{Code: "严重-1", Kind: "allow", Text: "直接获取服务器/系统权限（RCE、命令执行、SQLi 提权、root/集群权限等）"},
				{Code: "严重-2", Kind: "allow", Text: "获取服务级关键凭证（AK/SK/证书/密钥等），或泄露百万级以上重要敏感数据"},
				{Code: "高危-1", Kind: "allow", Text: "越权读取重要敏感数据（真实姓名、身份证、明文手机号、订单/资金/员工信息等），或可批量遍历"},
				{Code: "高危-2", Kind: "allow", Text: "重要业务系统的未授权写/删/改，且有回读证据"},
				{Code: "中危-1", Kind: "allow", Text: "一般越权/信息泄露，影响范围有限"},
				{Code: "低危-1", Kind: "allow", Text: "低敏感信息泄露或无明显实际影响的配置问题"},
				{Code: "忽略-1", Kind: "deny", Text: "仅理论可行、未实际验证、无实际影响；Self-XSS；无敏感信息的版本/指纹"},
				{Code: "忽略-2", Kind: "deny", Text: "无法影响他人的本地 DoS、纯 DNS 日志型 SSRF、SPF 邮件伪造等"},
			},
		},
		{
			nkey: "bank-src", name: "银行 SRC",
			desc: "银行/金融 SRC 示例模板，请按实际 SRC 标准调整。",
			clauses: []RatingTemplateClauseInput{
				{Code: "严重-1", Kind: "allow", Text: "获取银行核心系统权限（RCE/命令执行/提权），或可篡改资金/交易"},
				{Code: "严重-2", Kind: "allow", Text: "大规模客户敏感信息泄露（身份证、银行卡、交易流水、账户资金等）"},
				{Code: "高危-1", Kind: "allow", Text: "越权访问客户账户/资金/交易数据"},
				{Code: "高危-2", Kind: "allow", Text: "重要系统未授权写操作且有回读证据"},
				{Code: "中危-1", Kind: "allow", Text: "有限范围的客户信息泄露或一般越权"},
				{Code: "低危-1", Kind: "allow", Text: "低敏感信息泄露或配置问题"},
				{Code: "忽略-1", Kind: "deny", Text: "仅理论可行、无实际影响、第三方非银行资产"},
			},
		},
		{
			nkey: "redteam", name: "攻防",
			desc: "攻防演练示例模板，请按实际规则调整。",
			clauses: []RatingTemplateClauseInput{
				{Code: "得分-1", Kind: "allow", Text: "获取目标核心权限（域控/关键业务系统/生产主机）"},
				{Code: "得分-2", Kind: "allow", Text: "获取指定 flag 或目标数据"},
				{Code: "得分-3", Kind: "allow", Text: "突破边界进入内网并获得可用立足点"},
				{Code: "忽略-1", Kind: "deny", Text: "仅扫描/指纹识别/无实际权限获取"},
				{Code: "忽略-2", Kind: "deny", Text: "第三方或非目标资产"},
			},
		},
	}
	for _, sd := range seeds {
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		var id int64
		err = tx.QueryRow(`INSERT INTO rating_templates(nkey,name,description,builtin)
VALUES ($1,$2,$3,true) ON CONFLICT (nkey) DO NOTHING RETURNING id`, sd.nkey, sd.name, sd.desc).Scan(&id)
		if err == sql.ErrNoRows {
			_ = tx.Rollback()
			continue
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := insertTemplateClausesTx(tx, id, sd.clauses); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return d.SetSetting(flag, "true")
}
