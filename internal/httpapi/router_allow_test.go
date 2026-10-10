package httpapi

import "testing"

// Go's ServeMux reports the allowed methods sorted alphabetically; the reference
// emits them in http_method_names order with HEAD (injected by setup()) and
// OPTIONS always present. The differential harness records the header verbatim,
// so the wrapper must not sort it.
func TestNormalizeAllowUsesTheReferenceMethodOrder(t *testing.T) {
	cases := []struct {
		values []string
		want   string
	}{
		{[]string{"GET, HEAD, OPTIONS, PUT"}, "GET, PUT, HEAD, OPTIONS"},
		{[]string{"DELETE, GET, PUT"}, "GET, PUT, DELETE, HEAD, OPTIONS"},
		{[]string{"POST"}, "POST, HEAD, OPTIONS"},
		{[]string{"GET", "GET, POST"}, "GET, POST, HEAD, OPTIONS"},
		{[]string{"PATCH, HEAD"}, "PATCH, HEAD, OPTIONS"},
	}
	for _, testCase := range cases {
		if got := normalizeAllow(testCase.values); got != testCase.want {
			t.Fatalf("normalizeAllow(%v) = %q, want %q", testCase.values, got, testCase.want)
		}
	}
}
