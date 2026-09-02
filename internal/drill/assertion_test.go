package drill

import (
	"errors"
	"strings"
	"testing"
)

func TestEvaluateAssertion(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		err        error
		wantPassed bool
	}{
		{"single true (t)", "t", nil, true},
		{"single true (word)", "true", nil, true},
		{"single false", "f", nil, false},
		{"query error", "", errors.New("boom"), false},
		{"multi-statement output fails, not silently mismatched", "t\nf", nil, false},
		{"non-boolean output", "42", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := evaluateAssertion(postgresEngine, "my check", tc.out, tc.err)
			if res.Passed != tc.wantPassed {
				t.Errorf("evaluateAssertion(%q, %v).Passed = %v, want %v (details: %s)", tc.out, tc.err, res.Passed, tc.wantPassed, res.Details)
			}
		})
	}
}

func TestEvaluateAssertionMultiStatementDetailsAreHonest(t *testing.T) {
	res := evaluateAssertion(postgresEngine, "my check", "t\nf", nil)
	if res.Passed {
		t.Fatal("expected multi-line output to fail")
	}
	// Previously the Details field printed only firstLine(out) ("query
	// returned t"), which read as a contradiction next to a failed check.
	if !strings.Contains(res.Details, "2 lines") {
		t.Errorf("expected details to explain the multi-line mismatch, got %q", res.Details)
	}
}

// TestEvaluateAssertionNeverLeaksRowContent: a malformed assertion (missing
// WHERE) used to embed the raw query output verbatim, leaking data into
// reports/webhooks.
func TestEvaluateAssertionNeverLeaksRowContent(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"multi-row leak", "alice@example.com\nbob@example.com\ncarol@example.com"},
		{"single non-boolean row leak", "alice@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := evaluateAssertion(postgresEngine, "my check", tc.out, nil)
			if res.Passed {
				t.Fatal("expected non-boolean output to fail")
			}
			if strings.Contains(res.Details, "example.com") {
				t.Errorf("Details leaked row content: %q", res.Details)
			}
		})
	}
}
