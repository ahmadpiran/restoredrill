package drill

import (
	"errors"
	"testing"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

func TestExpectedCheckNamesCoversEveryConfiguredCheck(t *testing.T) {
	cfg := config.Checks{
		RTOTargetDuration: 30 * time.Minute,
		MinTables:         5,
		SequenceIntegrity: true,
		RowCounts:         []config.RowCount{{Table: "users", Min: 1}},
		Queries:           []config.Assertion{{Name: "no orphans", SQL: "select true"}},
	}
	names := expectedCheckNames(cfg)
	want := []string{
		"RTO target met (restore under 30m0s)",
		"at least 5 tables restored",
		"sequence integrity",
		"users has at least 1 rows",
		"no orphans",
	}
	if len(names) != len(want) {
		t.Fatalf("got %d names %v, want %d %v", len(names), names, len(want), want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d] = %q, want %q", i, names[i], w)
		}
	}
}

// A failed precheck used to abort Run() with downstream checks simply
// missing from rep.Checks. fail() must mark them explicitly instead.
func TestFailRecordsUnreachedChecksAsExplicitFailures(t *testing.T) {
	cfg := config.Checks{
		MinTables:         5,
		SequenceIntegrity: true,
		RowCounts:         []config.RowCount{{Table: "users", Min: 1}},
	}
	rep := &report.Report{
		Checks: []report.CheckResult{
			{Name: "precheck: RPO target met (backup fresher than 24h0m0s)", Passed: false, Details: "backup is 48h0m0s old"},
		},
	}
	rep, err := fail(rep, cfg, errors.New("RPO target check failed"))
	if err == nil {
		t.Fatal("expected fail to return the error unchanged")
	}
	if rep.Passed {
		t.Error("expected Passed=false after fail")
	}

	byName := map[string]report.CheckResult{}
	for _, c := range rep.Checks {
		byName[c.Name] = c
	}
	if len(rep.Checks) != 4 {
		t.Fatalf("expected the original RPO check plus 3 unreached checks, got %d: %+v", len(rep.Checks), rep.Checks)
	}
	for _, name := range []string{"at least 5 tables restored", "sequence integrity", "users has at least 1 rows"} {
		c, ok := byName[name]
		if !ok {
			t.Errorf("expected an explicit unreached-check entry for %q", name)
			continue
		}
		if c.Passed {
			t.Errorf("expected unreached check %q to be marked failed, not passed", name)
		}
		if c.Details == "" {
			t.Errorf("expected unreached check %q to explain why, got empty Details", name)
		}
	}
	// The precheck that actually ran and failed must not be duplicated.
	rpo := byName["precheck: RPO target met (backup fresher than 24h0m0s)"]
	if rpo.Details != "backup is 48h0m0s old" {
		t.Errorf("expected the original RPO check's own Details preserved, got %q", rpo.Details)
	}
}
