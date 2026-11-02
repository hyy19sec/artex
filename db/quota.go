package db

// HostQuotaUsage is one host:port's reported-finding counts, derived from
// findings.asset_ids → service assets. It lets the planner check per-host quotas
// without relying on hand-written "已报 12 个" hints.
type HostQuotaUsage struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Critical int    `json:"critical"`
	High     int    `json:"high"`
	Medium   int    `json:"medium"`
	Low      int    `json:"low"`
	Total    int    `json:"total"`
}

// HostQuotaUsage aggregates findings by the service host:port they are anchored
// to. hostFilter is an optional ILIKE over "domain:port" or domain. limit<=0 → 50,
// capped at 200. A finding anchored to multiple service assets counts once per
// host:port, which is the intended quota semantics.
func (s *ExplorationStore) HostQuotaUsage(hostFilter string, limit int) ([]HostQuotaUsage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query(`
WITH task AS (
  SELECT id FROM tasks WHERE exploration_id=$1 AND deleted_at IS NULL
),
fa AS (
  SELECT f.id, f.severity, a.domain, a.port
  FROM findings f
  JOIN task t ON t.id=f.task_id
  CROSS JOIN LATERAL jsonb_array_elements_text(f.asset_ids) aid
  JOIN assets a ON a.id = CASE WHEN aid ~ '^[0-9]+$' THEN aid::bigint END
  WHERE a.type='service' AND a.port IS NOT NULL
    AND ($2='' OR (a.domain || ':' || a.port) ILIKE '%' || $2 || '%' OR a.domain ILIKE '%' || $2 || '%')
)
SELECT domain, port,
       COUNT(*) FILTER (WHERE severity='critical') AS critical,
       COUNT(*) FILTER (WHERE severity='high')     AS high,
       COUNT(*) FILTER (WHERE severity='medium')   AS medium,
       COUNT(*) FILTER (WHERE severity='low')      AS low,
       COUNT(*) AS total
FROM fa
GROUP BY domain, port
ORDER BY total DESC, domain, port
LIMIT $3`, s.expID, hostFilter, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]HostQuotaUsage, 0)
	for rows.Next() {
		var u HostQuotaUsage
		if err := rows.Scan(&u.Host, &u.Port, &u.Critical, &u.High, &u.Medium, &u.Low, &u.Total); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
