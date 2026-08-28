package drill

import (
	"strings"
	"testing"
)

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

func TestUnexpectedGlobalsErrorsToleratesOnlyThePostgresRoleCollision(t *testing.T) {
	out := "SET\nCREATE ROLE\npsql:/tmp/globals.sql:5: ERROR:  role \"postgres\" already exists\nALTER ROLE\n"
	if bad := unexpectedGlobalsErrors(out); len(bad) != 0 {
		t.Errorf("expected the postgres role collision to be tolerated, got %v", bad)
	}
}

func TestUnexpectedGlobalsErrorsCatchesOtherErrors(t *testing.T) {
	out := "CREATE ROLE\npsql:/tmp/globals.sql:5: ERROR:  role \"app_reader\" already exists\nALTER ROLE\n"
	bad := unexpectedGlobalsErrors(out)
	if len(bad) != 1 {
		t.Fatalf("expected 1 unexpected error, got %v", bad)
	}
	if !strings.Contains(bad[0], "app_reader") {
		t.Errorf("expected the app_reader collision to be reported, got %q", bad[0])
	}
}
