package drill

import "testing"

func TestQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"users":        `"users"`,
		"public.users": `"public"."users"`,
		`weird"table`:  `"weird""table"`,
	}
	for in, want := range cases {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"one line", "one line"},
		{"first\nsecond\nthird", "first"},
		{"  leading and trailing space trimmed  ", "leading and trailing space trimmed"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
