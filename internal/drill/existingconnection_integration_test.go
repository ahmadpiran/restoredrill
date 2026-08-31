package drill

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
)

const existingConnectionTestPassword = "s3cr3t-pw"

// startExistingConnectionTarget stands in for "someone else's
// already-restored database". It publishes a port rather than using docker
// exec: a container IP isn't directly routable from a Windows host.
func startExistingConnectionTarget(t *testing.T) (dsn string, host string, port string) {
	t.Helper()
	name := fmt.Sprintf("restoredrill-test-existing-%d", time.Now().UnixNano())
	if out, err := run("docker", "run", "-d", "--name", name, "-p", "127.0.0.1:0:5432",
		"-e", "POSTGRES_PASSWORD="+existingConnectionTestPassword, "postgres:16"); err != nil {
		t.Fatalf("starting target container: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := run("docker", "rm", "-f", name); err != nil {
			t.Logf("warning: failed to remove test container %s: %v: %s", name, err, out)
		}
	})

	portOut, err := run("docker", "port", name, "5432")
	if err != nil {
		t.Fatalf("docker port: %v: %s", err, portOut)
	}
	binding := strings.TrimSpace(strings.Split(portOut, "\n")[0])
	i := strings.LastIndex(binding, ":")
	if i < 0 {
		t.Fatalf("unexpected docker port output: %q", portOut)
	}
	host, port = "127.0.0.1", binding[i+1:]

	setup := &sandbox{name: name}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := setup.exec("pg_isready", "-U", "postgres"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("target postgres not ready after 60s")
		}
		time.Sleep(time.Second)
	}

	execSQL := func(sql string) {
		t.Helper()
		if out, err := setup.exec("psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", sql); err != nil {
			t.Fatalf("setup SQL %q failed: %v: %s", sql, err, out)
		}
	}
	execSQL("CREATE TABLE granted (id int)")
	execSQL("INSERT INTO granted VALUES (1), (2)")
	execSQL("CREATE TABLE ungranted (id int)")
	execSQL("INSERT INTO ungranted VALUES (1)")
	execSQL("CREATE ROLE app_role")
	execSQL("GRANT SELECT ON granted TO app_role")

	dsn = fmt.Sprintf("postgres://postgres:%s@%s:%s/postgres?sslmode=disable",
		existingConnectionTestPassword, host, port)
	return dsn, host, port
}

func TestExistingConnectionMode(t *testing.T) {
	if out, err := run("docker", "info"); err != nil {
		t.Skipf("docker not available, skipping integration test: %v: %s", err, firstLine(out))
	}
	if _, err := exec.LookPath("psql"); err != nil {
		t.Skip("psql not available on host, skipping integration test")
	}

	dsn, _, _ := startExistingConnectionTarget(t)

	cfg := &config.Config{
		Backup: config.Backup{
			Format:        "existing_connection",
			ConnectionDSN: dsn,
		},
		Checks: config.Checks{
			RowCounts:    []config.RowCount{{Table: "granted", Min: 1}},
			VerifyAsRole: "app_role",
		},
	}

	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run failed: %v (report: %+v)", err, rep)
	}
	if !rep.Passed {
		t.Fatalf("expected the drill to pass, got checks: %+v", rep.Checks)
	}
	if rep.RestoreMethod != "existing_connection" {
		t.Errorf("expected RestoreMethod %q, got %q", "existing_connection", rep.RestoreMethod)
	}
	if rep.RestoreDurationSeconds != 0 {
		t.Errorf("expected RestoreDurationSeconds to stay 0 (no restore performed), got %v", rep.RestoreDurationSeconds)
	}
	if !rep.RestoreStartedAt.IsZero() || !rep.RestoreFinishedAt.IsZero() {
		t.Errorf("expected restore timestamps to stay zero, got started=%v finished=%v", rep.RestoreStartedAt, rep.RestoreFinishedAt)
	}
	if rep.PostgresImage != "" {
		t.Errorf("expected postgres_image to stay empty (no image is ever used), got %q", rep.PostgresImage)
	}
	if strings.Contains(rep.BackupSource, existingConnectionTestPassword) || strings.Contains(rep.BackupResolvedKey, existingConnectionTestPassword) {
		t.Errorf("expected the connection target fields to omit the password, got source=%q resolved_key=%q", rep.BackupSource, rep.BackupResolvedKey)
	}
	if !strings.Contains(rep.BackupResolvedKey, "postgres") {
		t.Errorf("expected the connection target to record something recognizable, got %q", rep.BackupResolvedKey)
	}
}

// TestExistingConnectionSurfacesMissingGrant proves verify_as_role catches a
// missing grant here too, with no globals_source needed.
func TestExistingConnectionSurfacesMissingGrant(t *testing.T) {
	if out, err := run("docker", "info"); err != nil {
		t.Skipf("docker not available, skipping integration test: %v: %s", err, firstLine(out))
	}
	if _, err := exec.LookPath("psql"); err != nil {
		t.Skip("psql not available on host, skipping integration test")
	}

	dsn, _, _ := startExistingConnectionTarget(t)

	cfg := &config.Config{
		Backup: config.Backup{
			Format:        "existing_connection",
			ConnectionDSN: dsn,
		},
		Checks: config.Checks{
			RowCounts: []config.RowCount{
				{Table: "granted", Min: 1},
				{Table: "ungranted", Min: 1},
			},
			VerifyAsRole: "app_role",
		},
	}

	// A failed check (as opposed to a failed precheck) doesn't make Run
	// return an error; it's reflected in rep.Passed and the check's own
	// Details, same as every other format.
	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run failed: %v (report: %+v)", err, rep)
	}
	if rep.Passed {
		t.Fatal("expected rep.Passed to be false: the ungranted row-count check should fail")
	}

	details := map[string]string{}
	for _, c := range rep.Checks {
		details[c.Name] = c.Details
	}
	if !strings.Contains(strings.ToLower(details["ungranted has at least 1 rows"]), "permission denied") {
		t.Errorf("expected permission-denied detail for ungranted table, got %q", details["ungranted has at least 1 rows"])
	}
}

// TestExistingConnectionSessionIsReadOnly proves a write assertion actually
// fails, rather than trusting that user SQL never writes.
func TestExistingConnectionSessionIsReadOnly(t *testing.T) {
	if out, err := run("docker", "info"); err != nil {
		t.Skipf("docker not available, skipping integration test: %v: %s", err, firstLine(out))
	}
	if _, err := exec.LookPath("psql"); err != nil {
		t.Skip("psql not available on host, skipping integration test")
	}

	dsn, _, _ := startExistingConnectionTarget(t)

	cfg := &config.Config{
		Backup: config.Backup{
			Format:        "existing_connection",
			ConnectionDSN: dsn,
		},
		Checks: config.Checks{
			Queries: []config.Assertion{
				{Name: "attempted write", SQL: "INSERT INTO granted VALUES (99) RETURNING true"},
			},
		},
	}

	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run failed: %v (report: %+v)", err, rep)
	}
	if rep.Passed {
		t.Fatal("expected rep.Passed to be false: the write assertion should fail")
	}

	var details string
	for _, c := range rep.Checks {
		if c.Name == "attempted write" {
			details = c.Details
		}
	}
	if !strings.Contains(strings.ToLower(details), "read-only") {
		t.Errorf("expected a read-only transaction error, got %q", details)
	}

	sb := newExistingConnectionSandbox(dsn)
	out, qerr := sb.query("SELECT count(*) FROM granted")
	if qerr != nil {
		t.Fatalf("verifying row count: %v", qerr)
	}
	if out != "2" {
		t.Errorf("expected the INSERT to have been blocked (still 2 rows), got %q", out)
	}
}
