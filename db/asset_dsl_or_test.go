package db

import "testing"

// TestDSLORGroupIsParenthesized pins the AND/OR precedence fix for callers that
// append "AND type = ..." / "AND id IN (scope)" to the DSL WHERE clause. Without
// the outer parentheses those filters only constrain the last OR branch, so an
// OR query with type=service would still return other asset types and could
// escape the task scope.
func TestDSLORGroupIsParenthesized(t *testing.T) {
	node, err := ParseDSL("domain=api OR domain=admin")
	if err != nil {
		t.Fatalf("ParseDSL: %v", err)
	}
	where, args, err := buildDSLWhere(node)
	if err != nil {
		t.Fatalf("buildDSLWhere: %v", err)
	}
	const wantWhere = "((domain ILIKE $1) OR (domain ILIKE $2))"
	if where != wantWhere {
		t.Fatalf("where = %q, want %q", where, wantWhere)
	}
	if len(args) != 2 || args[0] != "%api%" || args[1] != "%admin%" {
		t.Fatalf("args = %#v, want [%%api%% %%admin%%]", args)
	}

	// Mirrors the shape QueryDSL / QueryDSLInScope / CountDSL build: the
	// appended filter must apply to the whole OR group, not just the last branch.
	const wantCombined = "((domain ILIKE $1) OR (domain ILIKE $2)) AND type = $3"
	if combined := where + " AND type = $3"; combined != wantCombined {
		t.Fatalf("combined = %q, want %q", combined, wantCombined)
	}
}

// TestDSLTopLevelANDShape guards the existing AND behavior: every child is
// already parenthesized, so appending "AND ..." was never ambiguous there.
func TestDSLTopLevelANDShape(t *testing.T) {
	node, err := ParseDSL("has_js==true AND status_code<400")
	if err != nil {
		t.Fatalf("ParseDSL: %v", err)
	}
	where, _, err := buildDSLWhere(node)
	if err != nil {
		t.Fatalf("buildDSLWhere: %v", err)
	}
	const want = "(has_js = $1) AND (status_code < $2)"
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
}
