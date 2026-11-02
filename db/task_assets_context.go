package db

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// directTaskContextCTE is the current task plus exactly its explicitly related
// source tasks. It is deliberately non-recursive.
const directTaskContextCTE = `
context_tasks AS (
  SELECT t.id AS task_id, t.exploration_id
  FROM tasks t
  WHERE t.id=$1 AND t.deleted_at IS NULL
  UNION ALL
  SELECT source.id, source.exploration_id
  FROM task_relations relation
  JOIN tasks source ON source.id=relation.source_task_id AND source.deleted_at IS NULL
  WHERE relation.task_id=$1
)`

const contextCoverageCTE = directTaskContextCTE + `,
target AS (
  SELECT DISTINCT a.id, a.type, a.domain,
         COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, '') AS label,
         a.has_js, a.service_type, a.content_length, a.page_title, a.status_code
  FROM assets a
  JOIN task_scope ts ON (
       (ts.kind='company'     AND a.company_id = ts.company_id)
    OR (ts.kind='root_domain' AND a.root_domain = ts.domain)
    OR (ts.kind='subdomain'   AND a.domain = ts.domain)
    OR (ts.kind IN ('ip','cidr') AND ts.net >>= try_inet(a.ip))
    OR (ts.kind='icp' AND (
         lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = ts.value
         OR lower(regexp_replace(COALESCE(a.app_icp,''), '[[:space:]]+', '', 'g')) = ts.value
       ))
  )
  JOIN context_tasks ctx ON ctx.task_id=ts.task_id
	UNION
	SELECT a.id, a.type, a.domain,
	       COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, '') AS label,
	       a.has_js, a.service_type, a.content_length, a.page_title, a.status_code
	FROM assets a
	JOIN exploration_anchors ea ON ea.asset_id=a.id
	JOIN exploration_nodes en ON en.id=ea.node_id
	JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
),
tested AS (
  SELECT DISTINCT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id AND en.kind='fact'
  JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
)`

// scopeTargetCTE selects every asset that BELONGS to the current task's (and its
// direct source tasks') declared scope — membership, not literal value: a
// root_domain scope pulls in every subdomain / service / endpoint whose own
// root_domain column equals it; an ip/cidr scope pulls in assets whose ip OR
// IP-literal host falls inside the net. $1 is the task id. Unlike contextCoverageCTE
// it carries neither the fact-anchor union nor the tested set — it is pure "in
// declared scope", independent of what has already been touched. Used by the
// agent's list_assets so a query returns the task's relevant assets, not the
// whole shared库.
const scopeTargetCTE = `
context_tasks AS (
  SELECT t.id AS task_id
  FROM tasks t
  WHERE t.id=$1 AND t.deleted_at IS NULL
  UNION ALL
  SELECT source.id
  FROM task_relations relation
  JOIN tasks source ON source.id=relation.source_task_id AND source.deleted_at IS NULL
  WHERE relation.task_id=$1
),
target AS (
  SELECT DISTINCT a.id
  FROM assets a
  JOIN task_scope ts ON (
       (ts.kind='company'     AND a.company_id = ts.company_id)
    OR (ts.kind='root_domain' AND a.root_domain = ts.domain)
    OR (ts.kind='subdomain'   AND a.domain = ts.domain)
    OR (ts.kind IN ('ip','cidr') AND (ts.net >>= try_inet(a.ip) OR ts.net >>= try_inet(a.domain)))
    OR (ts.kind='icp' AND (
         lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = ts.value
         OR lower(regexp_replace(COALESCE(a.app_icp,''), '[[:space:]]+', '', 'g')) = ts.value
       ))
  )
  JOIN context_tasks ctx ON ctx.task_id=ts.task_id
)`

// scopeTargetCTEMaterialized is scopeTargetCTE with both CTEs forced to
// materialize. QueryDSLInScope uses it because a leading-wildcard filter such as
// page_title ILIKE '%管理%' makes the planner badly underestimate the outer row
// count; without MATERIALIZED it re-evaluates the whole target scope set for
// every outer candidate (34 × 121k rows in one observed round), turning a
// sub-second lookup into minutes. GetByIDsInScope keeps the non-materialized
// form: its id list is tiny and a correlated lookup is cheaper there.
var scopeTargetCTEMaterialized = strings.Replace(
	strings.Replace(scopeTargetCTE, "context_tasks AS (", "context_tasks AS MATERIALIZED (", 1),
	"target AS (", "target AS MATERIALIZED (", 1,
)

// ListTaskScopeWithSources returns the current task's scope followed by the
// scopes of its direct source tasks. TaskScope.TaskID preserves provenance.
func (s *AssetStore) ListTaskScopeWithSources(taskID int64) ([]TaskScope, error) {
	rows, err := s.db.Query(`WITH `+directTaskContextCTE+`
SELECT ts.id, ts.task_id, ts.kind, COALESCE(ts.company_id,0), COALESCE(c.name,''), COALESCE(ts.domain,''),
       COALESCE(ts.net::text,''), COALESCE(ts.value,''), ts.source, COALESCE(ts.reason,'')
FROM task_scope ts
JOIN context_tasks ctx ON ctx.task_id=ts.task_id
LEFT JOIN companies c ON c.id=ts.company_id
ORDER BY CASE WHEN ts.task_id=$1 THEN 0 ELSE 1 END, ts.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskScope{}
	for rows.Next() {
		var scope TaskScope
		var companyID int64
		if err := rows.Scan(&scope.ID, &scope.TaskID, &scope.Kind, &companyID, &scope.CompanyName, &scope.Domain, &scope.Net, &scope.Value, &scope.Source, &scope.Reason); err != nil {
			return nil, err
		}
		if companyID > 0 {
			scope.CompanyID = &companyID
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}

// TaskCoverageWithSources computes one coverage view over the union of the
// current task and its direct sources: source scopes and anchored assets extend
// the denominator, while fact anchors count as tested. No row is copied.
func (s *AssetStore) TaskCoverageWithSources(taskID int64) (*Coverage, error) {
	cov := &Coverage{ByType: []CoverageByType{}}
	_ = s.db.QueryRow(`WITH `+directTaskContextCTE+`
SELECT count(*) FROM task_scope ts JOIN context_tasks ctx ON ctx.task_id=ts.task_id`, taskID).Scan(&cov.ScopeRows)
	rows, err := s.db.Query(`WITH `+contextCoverageCTE+`
SELECT target.type, count(*) AS total,
       count(*) FILTER (WHERE target.id IN (SELECT asset_id FROM tested)) AS tested,
       count(*) FILTER (WHERE target.type='service' AND target.service_type='http' AND target.has_js IS TRUE) AS has_js_total,
       count(*) FILTER (WHERE target.type='service' AND target.service_type='http' AND target.has_js IS TRUE AND target.id IN (SELECT asset_id FROM tested)) AS has_js_tested
FROM target GROUP BY target.type ORDER BY target.type`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var byType CoverageByType
		var jsTotal, jsTested int
		if err := rows.Scan(&byType.Type, &byType.Total, &byType.Tested, &jsTotal, &jsTested); err != nil {
			return nil, err
		}
		cov.ByType = append(cov.ByType, byType)
		cov.Denominator += byType.Total
		cov.Tested += byType.Tested
		cov.HasJSTotal += jsTotal
		cov.HasJSTested += jsTested
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cov.Denominator > 0 {
		pct := float64(cov.Tested) / float64(cov.Denominator)
		cov.Pct = &pct
	}
	if cov.HasJSTotal > 0 {
		pct := float64(cov.HasJSTested) / float64(cov.HasJSTotal)
		cov.HasJSPct = &pct
	}
	return cov, nil
}

// ListUntestedAssetsWithSources is the direct-source-aware backlog query used
// by inherited tasks. Source fact anchors remove assets from the backlog.
func (s *AssetStore) ListUntestedAssetsWithSources(taskID int64, typ string, limit, offset int) ([]CoverageAsset, int, error) {
	return s.ListUntestedAssetsWithSourcesExcluding(taskID, typ, nil, limit, offset)
}

// ListUntestedAssetsWithSourcesExcluding is ListUntestedAssetsWithSources with
// an optional list of domain suffixes to exclude from both the count and the
// page. Suffixes are matched against the asset's host column (not url): either
// the bare host equals the suffix or the host ends with ".<suffix>". This is
// used to drop closed asset families (e.g. aedev.feishu.cn) without excluding
// their whole root domain. A nil/empty list keeps the original behavior.
func (s *AssetStore) ListUntestedAssetsWithSourcesExcluding(taskID int64, typ string, excludeDomainSuffixes []string, limit, offset int) ([]CoverageAsset, int, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	typeFilter := ""
	baseArgs := []any{taskID}
	if typ != "" {
		typeFilter = " AND target.type = $2"
		baseArgs = append(baseArgs, typ)
	}
	// Only a type=service request exposes the HTTP-specific fields (and uses the
	// has_js/content_length ordering); every other request is plain id order.
	eligible := "FALSE"
	if typ == "service" {
		eligible = "(target.type='service' AND target.label ~* '^https?://')"
	}
	excludeClause := ""
	if suffixes := normalizeDomainSuffixes(excludeDomainSuffixes); len(suffixes) > 0 {
		baseArgs = append(baseArgs, suffixes)
		excludeClause = " AND NOT EXISTS (SELECT 1 FROM unnest($" + strconv.Itoa(len(baseArgs)) + "::text[]) sfx WHERE lower(target.domain) = sfx OR right(lower(target.domain), length(sfx) + 1) = '.' || sfx)"
	}
	var total int
	if err := s.db.QueryRow(`WITH `+contextCoverageCTE+`
SELECT count(*) FROM target WHERE target.id NOT IN (SELECT asset_id FROM tested)`+typeFilter+excludeClause, baseArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageArgs := append(append([]any{}, baseArgs...), limit, offset)
	limitPosition := strconv.Itoa(len(baseArgs) + 1)
	offsetPosition := strconv.Itoa(len(baseArgs) + 2)
	rows, err := s.db.Query(`WITH `+contextCoverageCTE+`
SELECT target.id, target.type, target.label,
       CASE WHEN `+eligible+` THEN target.has_js END,
       CASE WHEN `+eligible+` THEN target.content_length END,
       CASE WHEN `+eligible+` THEN target.page_title END,
       CASE WHEN `+eligible+` THEN target.status_code END
FROM target
WHERE target.id NOT IN (SELECT asset_id FROM tested)`+typeFilter+excludeClause+`
ORDER BY CASE WHEN `+eligible+` THEN target.has_js END DESC NULLS LAST,
         CASE WHEN `+eligible+` THEN target.content_length END DESC NULLS LAST,
         target.id
LIMIT $`+limitPosition+` OFFSET $`+offsetPosition, pageArgs...)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	out := []CoverageAsset{}
	for rows.Next() {
		var asset CoverageAsset
		var hasJS *bool
		var contentLength *int64
		var pageTitle *string
		var statusCode *int
		if err := rows.Scan(&asset.ID, &asset.Type, &asset.Label, &hasJS, &contentLength, &pageTitle, &statusCode); err != nil {
			return nil, total, err
		}
		asset.HasJS = hasJS
		asset.ContentLength = contentLength
		asset.PageTitle = pageTitle
		asset.StatusCode = statusCode
		out = append(out, asset)
	}
	return out, total, rows.Err()
}

// normalizeDomainSuffixes lowercases, trims, dedupes and canonicalizes domain
// suffixes for ListUntestedAssetsWithSourcesExcluding. Both "aedev.feishu.cn"
// and "*.aedev.feishu.cn" are accepted; the stored form has no leading dot or
// wildcard so the SQL can compare "= sfx" for the bare host and the ".sfx" tail
// for a subdomain of it.
func normalizeDomainSuffixes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		s = strings.TrimPrefix(s, "*.")
		s = strings.TrimPrefix(s, ".")
		s = strings.TrimSuffix(s, ".")
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// HostsByTaskWithSources resolves exact HTTP host candidates from assets that
// are attached to, anchored by, or in scope for the current task or a direct
// source. Traffic remains global and is not copied. This read helper must not be
// used for destructive task cleanup; HostsByTask intentionally retains that
// narrower, task-owned behavior.
func (s *AssetStore) HostsByTaskWithSources(taskID int64) ([]string, error) {
	rows, err := s.db.Query(`WITH `+directTaskContextCTE+`,
context_assets AS (
  SELECT DISTINCT a.id
  FROM assets a
  WHERE EXISTS (SELECT 1 FROM context_tasks ctx WHERE ctx.task_id=ANY(a.task_ids))
  UNION
  SELECT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id
  JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
  UNION
  SELECT DISTINCT a.id
  FROM assets a
  JOIN task_scope ts ON (
       (ts.kind='company'     AND a.company_id=ts.company_id)
    OR (ts.kind='root_domain' AND a.root_domain=ts.domain)
    OR (ts.kind='subdomain'   AND a.domain=ts.domain)
    OR (ts.kind IN ('ip','cidr') AND ts.net >>= try_inet(a.ip))
    OR (ts.kind='icp' AND (
         lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = ts.value
         OR lower(regexp_replace(COALESCE(a.app_icp,''), '[[:space:]]+', '', 'g')) = ts.value
       ))
  )
  JOIN context_tasks ctx ON ctx.task_id=ts.task_id
)
SELECT COALESCE(a.domain,''), COALESCE(a.ip,''), COALESCE(a.url,'')
FROM assets a JOIN context_assets ctx ON ctx.id=a.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := map[string]struct{}{}
	add := func(host string) {
		host = strings.TrimSpace(strings.ToLower(host))
		if host != "" {
			hosts[host] = struct{}{}
		}
	}
	for rows.Next() {
		var domain, ip, rawURL string
		if err := rows.Scan(&domain, &ip, &rawURL); err != nil {
			return nil, err
		}
		add(domain)
		add(ip)
		if parsed, err := url.Parse(rawURL); err == nil {
			add(parsed.Hostname())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hosts))
	for host := range hosts {
		out = append(out, host)
	}
	sort.Strings(out)
	return out, nil
}
