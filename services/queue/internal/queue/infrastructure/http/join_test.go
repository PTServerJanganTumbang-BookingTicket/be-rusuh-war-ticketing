package http

import "testing"

func TestBearerToken(t *testing.T) {
	cases := []struct {
		in   string
		tok  string
		okay bool
	}{
		{"", "", true},
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"bearer abc", "abc", true},
		{"Bearer    ", "", false}, // whitespace only: rejected, not a silent guest
		{"Basic dXNlcg==", "", false},
		{"Bearer", "", false},
	}
	for _, c := range cases {
		tok, ok := bearerToken(c.in)
		if tok != c.tok || ok != c.okay {
			t.Errorf("bearerToken(%q) = %q, %v; want %q, %v", c.in, tok, ok, c.tok, c.okay)
		}
	}
}
