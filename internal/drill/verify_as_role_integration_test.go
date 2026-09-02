package drill

import (
	"strings"
	"testing"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
)

// Real-Postgres integration test: seeds a role with a grant on one table but
// not another, then confirms runChecks under verify_as_role actually
// surfaces the missing grant as a check failure end to end (SET ROLE plus
// the existing fail-closed err != nil handling), not just that it's wired up.
func TestVerifyAsRoleSurfacesMissingGrant(t *testing.T) {
	if out, err := run("docker", "info"); err != nil {
		t.Skipf("docker not available, skipping integration test: %v: %s", err, firstLine(out))
	}

	sb := newSandbox(postgresEngine, "postgres:16", 120*time.Second, nil)
	if err := sb.start(); err != nil {
		t.Fatalf("starting sandbox: %v", err)
	}
	t.Cleanup(func() {
		if err := sb.destroy(); err != nil {
			t.Logf("warning: failed to destroy test container %s: %v", sb.name, err)
		}
	})

	execSQL := func(sql string) {
		t.Helper()
		if out, err := sb.exec("psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", sql); err != nil {
			t.Fatalf("setup SQL %q failed: %v: %s", sql, err, out)
		}
	}

	execSQL("CREATE ROLE app_role")
	execSQL("CREATE TABLE granted (id int)")
	execSQL("INSERT INTO granted VALUES (1), (2)")
	execSQL("CREATE TABLE ungranted (id int)")
	execSQL("INSERT INTO ungranted VALUES (1)")
	execSQL("GRANT SELECT ON granted TO app_role")

	cfg := config.Checks{
		RowCounts: []config.RowCount{
			{Table: "granted", Min: 1},
			{Table: "ungranted", Min: 1},
		},
		Queries: []config.Assertion{
			{Name: "granted table is readable", SQL: "SELECT count(*) >= 0 FROM granted"},
			{Name: "ungranted table is readable", SQL: "SELECT count(*) >= 0 FROM ungranted"},
		},
		VerifyAsRole: "app_role",
	}
	results := runChecks(sb, cfg)

	byName := make(map[string]bool, len(results))
	details := make(map[string]string, len(results))
	for _, r := range results {
		byName[r.Name] = r.Passed
		details[r.Name] = r.Details
	}

	if !byName["granted has at least 1 rows"] {
		t.Errorf("expected row-count check on granted table to pass, got %q", details["granted has at least 1 rows"])
	}
	if byName["ungranted has at least 1 rows"] {
		t.Error("expected row-count check on ungranted table to fail (no grant), but it passed")
	} else if !strings.Contains(strings.ToLower(details["ungranted has at least 1 rows"]), "permission denied") {
		t.Errorf("expected permission-denied detail for ungranted table, got %q", details["ungranted has at least 1 rows"])
	}

	if !byName["granted table is readable"] {
		t.Errorf("expected assertion on granted table to pass, got %q", details["granted table is readable"])
	}
	if byName["ungranted table is readable"] {
		t.Error("expected assertion on ungranted table to fail (no grant), but it passed")
	}
}
