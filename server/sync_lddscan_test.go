package server

import "testing"

func TestIsTimeoutError(t *testing.T) {
	for _, v := range []string{
		"Timeout('Failed to perform, curl: (28) Connection timed out')",
		"timeout",
		"ConnectTimeout('...')",
	} {
		if !isTimeoutError(v) {
			t.Fatalf("expected %q to be treated as a timeout", v)
		}
	}
	for _, v := range []string{"", "None", "ConnectionError('...')", "DNSError('...')"} {
		if isTimeoutError(v) {
			t.Fatalf("expected %q NOT to be treated as a timeout", v)
		}
	}
}

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
