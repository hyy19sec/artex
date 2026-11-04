package db

import "testing"

func TestNormalizeTemplateClauseScope(t *testing.T) {
	c, err := normalizeTemplateClause(RatingTemplateClauseInput{Text: "默认"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Scope != "rating" || c.Kind != "allow" {
		t.Fatalf("defaults = scope %q kind %q, want rating/allow", c.Scope, c.Kind)
	}

	c, err = normalizeTemplateClause(RatingTemplateClauseInput{Text: "操作", Scope: "OPERATION", Kind: "DENY"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Scope != "operation" || c.Kind != "deny" {
		t.Fatalf("normalized = scope %q kind %q, want operation/deny", c.Scope, c.Kind)
	}

	if _, err := normalizeTemplateClause(RatingTemplateClauseInput{Text: "x", Scope: "report"}); err == nil {
		t.Fatal("scope=report should be rejected")
	}

	if got := templateClauseText(RatingTemplateClauseInput{Code: "严重-1", Scope: "rating", Text: "评级"}); got != "【严重-1】评级" {
		t.Fatalf("rating text = %q", got)
	}
	if got := templateClauseText(RatingTemplateClauseInput{Code: "忽略", Scope: "operation", Text: "操作"}); got != "操作" {
		t.Fatalf("operation text = %q, want plain text", got)
	}
}

// TestRatingTemplateScopeSnapshot covers the assessment-template flow:
// rating/operation clauses snapshot with their own scope, replace keeps
// non-template operation constraints, and AddConstraint dedupes identical rows.
func TestRatingTemplateScopeSnapshot(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	tpl1, err := d.CreateRatingTemplate(RatingTemplateInput{
		Name: "测试测评模板A",
		Clauses: []RatingTemplateClauseInput{
			{Code: "高危-1", Kind: "allow", Scope: "rating", Text: "评级A"},
			{Kind: "deny", Scope: "operation", Text: "操作A"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM rating_templates WHERE id=$1`, tpl1.ID) //nolint:errcheck

	expID, err := d.CreateExploration("test", "测评模板 scope")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) //nolint:errcheck

	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshotRatingTemplateTx(tx, expID, tpl1.ID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	es := d.Exploration(expID)
	rows, err := es.ListConstraints()
	if err != nil {
		t.Fatal(err)
	}
	if got := constraintText(rows, "评级A"); got != "【高危-1】评级A" {
		t.Fatalf("rating snapshot = %q", got)
	}
	if got := constraintScope(rows, "操作A"); got != "operation" {
		t.Fatalf("operation snapshot scope = %q", got)
	}

	// A human operation constraint must survive a template replace.
	if _, err := es.AddConstraint("deny", "operation", "人工约束", "human", 0); err != nil {
		t.Fatal(err)
	}
	tpl2, err := d.CreateRatingTemplate(RatingTemplateInput{
		Name: "测试测评模板B",
		Clauses: []RatingTemplateClauseInput{
			{Code: "中危-1", Kind: "allow", Scope: "rating", Text: "评级B"},
			{Kind: "deny", Scope: "operation", Text: "操作B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM rating_templates WHERE id=$1`, tpl2.ID) //nolint:errcheck
	if _, err := d.ApplyRatingTemplate(expID, tpl2.ID, true); err != nil {
		t.Fatal(err)
	}

	rows, err = es.ListConstraints()
	if err != nil {
		t.Fatal(err)
	}
	if got := constraintStatus(rows, "操作A"); got != "superseded" {
		t.Fatalf("old template operation status = %q, want superseded", got)
	}
	if got := constraintStatus(rows, "人工约束"); got != "active" {
		t.Fatalf("human operation status = %q, want active", got)
	}
	if got := constraintStatus(rows, "操作B"); got != "active" {
		t.Fatalf("new template operation status = %q, want active", got)
	}
	if got := constraintStatus(rows, "评级A"); got != "superseded" {
		t.Fatalf("old rating status = %q, want superseded", got)
	}

	// Same (scope, kind, text) returns the existing active row id.
	id1, err := es.AddConstraint("deny", "operation", "人工约束", "human", 0)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := es.AddConstraint("deny", "operation", "人工约束", "goals", 0)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("dedupe ids = %d/%d, want same", id1, id2)
	}
}

func constraintText(rows []Constraint, text string) string {
	for _, c := range rows {
		if c.Text == text || c.Text == "【高危-1】"+text {
			return c.Text
		}
	}
	return ""
}

func constraintScope(rows []Constraint, text string) string {
	for _, c := range rows {
		if c.Text == text {
			return c.Scope
		}
	}
	return ""
}

func constraintStatus(rows []Constraint, text string) string {
	for _, c := range rows {
		if c.Text == text {
			return c.Status
		}
	}
	return ""
}
