package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Constraint is one task-scoped constraint. kind is allow|deny; scope separates
// two different things that used to be mixed into one allow/deny list:
//   - operation: what the operator may/may not do (authorization boundary);
//   - rating:    how to grade a finding (reference only, NOT authorization).
//
// status=superseded rows stay in history but are not injected into prompts.
type Constraint struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`  // allow | deny
	Scope        string    `json:"scope"` // operation | rating
	Text         string    `json:"text"`
	Origin       string    `json:"origin,omitempty"` // goals | human | system
	Status       string    `json:"status"`           // active | superseded
	SupersedesID *int64    `json:"supersedes_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func normalizeConstraintScope(scope string) (string, error) {
	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		return "operation", nil
	}
	switch scope {
	case "operation", "rating":
		return scope, nil
	default:
		return "", fmt.Errorf("scope 必须是 operation 或 rating")
	}
}

// ListConstraints returns all of this exploration's constraints, active first,
// then superseded history; oldest first within each group/scope/kind.
func (s *ExplorationStore) ListConstraints() ([]Constraint, error) {
	rows, err := s.db.Query(`
SELECT id, kind, scope, text, COALESCE(origin,''), status, supersedes_id, created_at
FROM task_constraints WHERE exploration_id=$1
ORDER BY (status='superseded'), (scope='operation') DESC, (scope='rating') DESC, (kind='deny'), id`, s.expID)
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

// AddConstraint inserts one constraint and returns its id. When supersedesID > 0
// the old constraint is marked superseded in the same transaction, so a prompt
// never sees both versions as active.
func (s *ExplorationStore) AddConstraint(kind, scope, text, origin string, supersedesID int64) (int64, error) {
	if kind != "allow" && kind != "deny" {
		return 0, fmt.Errorf("kind 必须是 allow 或 deny")
	}
	scope, err := normalizeConstraintScope(scope)
	if err != nil {
		return 0, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("text 不能为空")
	}
	if origin == "" {
		origin = "system"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	var id int64
	err = tx.QueryRow(`
INSERT INTO task_constraints(exploration_id, kind, scope, text, origin, status, supersedes_id)
VALUES ($1, $2, $3, $4, $5, 'active', NULLIF($6,0))
RETURNING id`, s.expID, kind, scope, text, origin, supersedesID).Scan(&id)
	if err != nil {
		return 0, err
	}
	if supersedesID > 0 {
		res, err := tx.Exec(`
UPDATE task_constraints SET status='superseded', updated_at=now()
WHERE id=$1 AND exploration_id=$2`, supersedesID, s.expID)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, fmt.Errorf("被替代的约束 %d 不存在", supersedesID)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateConstraint rewrites a constraint's kind/text and optionally its scope.
// An empty scope keeps the existing one; scoped to this exploration.
func (s *ExplorationStore) UpdateConstraint(id int64, kind, scope, text string) error {
	if kind != "allow" && kind != "deny" {
		return fmt.Errorf("kind 必须是 allow 或 deny")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text 不能为空")
	}
	var scopeVal any
	if strings.TrimSpace(scope) != "" {
		v, err := normalizeConstraintScope(scope)
		if err != nil {
			return err
		}
		scopeVal = v
	}
	res, err := s.db.Exec(`
UPDATE task_constraints SET kind=$1, text=$2, scope=COALESCE($3, scope), updated_at=now()
WHERE id=$4 AND exploration_id=$5`, kind, text, scopeVal, id, s.expID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("约束不存在")
	}
	return nil
}

// DeleteConstraint removes a constraint; scoped to this exploration. Returns an
// error if no such constraint exists.
func (s *ExplorationStore) DeleteConstraint(id int64) error {
	res, err := s.db.Exec(`DELETE FROM task_constraints WHERE id=$1 AND exploration_id=$2`, id, s.expID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("约束不存在")
	}
	return nil
}
