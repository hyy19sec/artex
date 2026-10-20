package db

import "testing"

// TestDSLHasJS pins the boolean DSL field used by the lddscan crawl_flag mapping.
func TestDSLHasJS(t *testing.T) {
	cases := []struct {
		dsl   string
		where string
		want  bool
	}{
		{"has_js==true", "has_js = $1", true},
		{"has_js=true", "has_js = $1", true},
		{"has_js==1", "has_js = $1", true},
		{"has_js!=false", "has_js != $1", false},
	}
	for _, tc := range cases {
		node, err := ParseDSL(tc.dsl)
		if err != nil {
			t.Fatalf("ParseDSL(%q): %v", tc.dsl, err)
		}
		where, args, err := buildDSLWhere(node)
		if err != nil {
			t.Fatalf("buildDSLWhere(%q): %v", tc.dsl, err)
		}
		if where != tc.where {
			t.Fatalf("%q: where = %q, want %q", tc.dsl, where, tc.where)
		}
		if len(args) != 1 || args[0] != tc.want {
			t.Fatalf("%q: args = %#v, want [%v]", tc.dsl, args, tc.want)
		}
	}

	node, err := ParseDSL("has_js==banana")
	if err != nil {
		t.Fatalf("ParseDSL should accept the string value: %v", err)
	}
	if _, _, err := buildDSLWhere(node); err == nil {
		t.Fatal("has_js==banana should be rejected by the builder")
	}
}
