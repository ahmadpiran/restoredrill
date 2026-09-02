package drill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
)

const mysqlTestImage = "mysql:8"

// mysqldump does not dump users, so a routine whose DEFINER only exists in
// the source restores clean and fails only when invoked. Proves the check
// catches that while every other check on the same restore passes.
func TestMysqlRestoreSurfacesMissingDefiner(t *testing.T) {
	if out, err := run("docker", "info"); err != nil {
		t.Skipf("docker not available, skipping integration test: %v: %s", err, firstLine(out))
	}

	src := newMySQLSandbox(mysqlTestImage, "appdb", 3*time.Minute)
	if err := src.startMySQL(); err != nil {
		t.Fatalf("starting source sandbox: %v", err)
	}
	t.Cleanup(func() {
		if err := src.destroy(); err != nil {
			t.Logf("warning: failed to destroy source container %s: %v", src.name, err)
		}
	})

	execSQL := func(sql string) {
		t.Helper()
		if out, err := src.execEnv(mysqlEnv(), "mysql", "-u", "root", "-e", sql); err != nil {
			t.Fatalf("setup SQL %q failed: %v: %s", sql, err, out)
		}
	}

	execSQL("CREATE DATABASE appdb")
	execSQL("CREATE USER 'app_owner'@'%' IDENTIFIED BY 'x'; GRANT ALL ON appdb.* TO 'app_owner'@'%'")
	execSQL("CREATE TABLE appdb.users (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50))")
	execSQL("INSERT INTO appdb.users (name) VALUES ('a'),('b'),('c')")
	execSQL("CREATE TABLE appdb.orders (id INT AUTO_INCREMENT PRIMARY KEY, user_id INT)")
	execSQL("INSERT INTO appdb.orders (user_id) VALUES (1),(2)")
	execSQL("CREATE VIEW appdb.active_users AS SELECT id, name FROM appdb.users")
	execSQL("CREATE DEFINER='app_owner'@'%' PROCEDURE appdb.count_users() SQL SECURITY DEFINER SELECT count(*) FROM appdb.users")

	definerCheck := config.Checks{DefinerIntegrity: true}
	for _, res := range runChecks(src, definerCheck) {
		if !res.Passed {
			t.Errorf("definer integrity should pass in the source, where app_owner exists: %s", res.Details)
		}
	}

	const remoteDump = "/tmp/restoredrill-test-dump.sql"
	if out, err := src.execEnv(mysqlEnv(), "sh", "-c",
		"mysqldump -u root --routines --triggers --events --databases appdb > "+remoteDump); err != nil {
		t.Fatalf("mysqldump failed: %v: %s", err, out)
	}
	localDump := filepath.Join(t.TempDir(), "appdb.sql")
	if out, err := run("docker", "cp", src.name+":"+remoteDump, localDump); err != nil {
		t.Fatalf("copying dump out of the source container: %v: %s", err, out)
	}
	if fi, err := os.Stat(localDump); err != nil || fi.Size() == 0 {
		t.Fatalf("expected a non-empty dump on the host: %v", err)
	}

	cfg := &config.Config{
		Backup:  config.Backup{Source: localDump, Format: "mysqldump_sql"},
		MySQL:   config.MySQL{Image: mysqlTestImage, Database: "appdb"},
		Sandbox: config.Sandbox{ReadyTimeoutDuration: 3 * time.Minute},
		Checks: config.Checks{
			MinTables:        2,
			DefinerIntegrity: true,
			RowCounts:        []config.RowCount{{Table: "users", Min: 3}},
			Queries: []config.Assertion{
				{Name: "orders survived the restore", SQL: "SELECT count(*) >= 2 FROM orders"},
			},
		},
	}

	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("drill returned a fatal error, expected a completed run with one failed check: %v", err)
	}
	if rep.MySQLImage != mysqlTestImage {
		t.Errorf("MySQLImage = %q, want %q", rep.MySQLImage, mysqlTestImage)
	}
	if rep.PostgresImage != "" {
		t.Errorf("expected PostgresImage to stay empty for a MySQL drill, got %q", rep.PostgresImage)
	}
	if rep.RestoreDurationSeconds <= 0 {
		t.Error("expected the restore to be timed")
	}

	passed := make(map[string]bool, len(rep.Checks))
	details := make(map[string]string, len(rep.Checks))
	for _, c := range rep.Checks {
		passed[c.Name] = c.Passed
		details[c.Name] = c.Details
	}

	for _, name := range []string{
		"precheck: dump file complete (trailer present)",
		"at least 2 tables restored",
		"users has at least 3 rows",
		"orders survived the restore",
	} {
		if _, ok := passed[name]; !ok {
			t.Errorf("expected check %q to have run, got %v", name, rep.Checks)
			continue
		}
		if !passed[name] {
			t.Errorf("expected check %q to pass on a restore that really worked: %s", name, details[name])
		}
	}

	if passed["definer integrity"] {
		t.Error("expected the missing definer to fail the drill, not pass silently")
	} else if !strings.Contains(details["definer integrity"], "count_users") {
		t.Errorf("expected the broken routine to be named, got %q", details["definer integrity"])
	}
	if rep.Passed {
		t.Error("expected the drill to fail overall")
	}
}

func TestMysqlDrillReportsUnevaluatedChecksWhenTheBackupIsMissing(t *testing.T) {
	cfg := &config.Config{
		Backup:  config.Backup{Source: filepath.Join(t.TempDir(), "nope.sql"), Format: "mysqldump_sql"},
		MySQL:   config.MySQL{Image: mysqlTestImage, Database: "appdb"},
		Sandbox: config.Sandbox{ReadyTimeoutDuration: time.Minute},
		Checks:  config.Checks{MinTables: 1, DefinerIntegrity: true},
	}
	rep, err := Run(cfg)
	if err == nil {
		t.Fatal("expected a missing backup file to fail the drill")
	}
	want := map[string]bool{"at least 1 tables restored": false, "definer integrity": false}
	for _, c := range rep.Checks {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
			if c.Passed {
				t.Errorf("check %q must not pass when the drill aborted", c.Name)
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected %q to be recorded as unevaluated, got %v", name, rep.Checks)
		}
	}
}
