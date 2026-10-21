package server

import "testing"

func TestIsCertErrorMisreport(t *testing.T) {
	for _, v := range []string{"误报", " 误报 ", "誤報"} {
		if !isCertErrorMisreport(v) {
			t.Fatalf("expected %q to be treated as a cert-error misreport", v)
		}
	}
	for _, v := range []string{"", "None", "null", "true", "证书错误", "误报?"} {
		if isCertErrorMisreport(v) {
			t.Fatalf("expected %q NOT to be treated as a cert-error misreport", v)
		}
	}
}
