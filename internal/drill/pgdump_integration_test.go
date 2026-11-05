package drill

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
)

// Real pg_dump -Fc of a database named orders_prod, restored by Run: the
// report must carry that name without the user configuring it anywhere.
func TestPgDumpCustomReportNamesTheDatabase(t *testing.T) {
	if out, err := run("docker", "info"); err != nil {
		t.Skipf("docker not available, skipping integration test: %v: %s", err, firstLine(out))
	}

	src := newSandbox(postgresEngine, "postgres:16", 3*time.Minute, nil)
	if err := src.start(); err != nil {
		t.Fatalf("starting source sandbox: %v", err)
	}
	t.Cleanup(func() {
		if err := src.destroy(); err != nil {
			t.Logf("warning: failed to destroy source container %s: %v", src.name, err)
		}
	})

	for _, args := range [][]string{
		{"psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-c", "CREATE DATABASE orders_prod"},
		{"psql", "-U", "postgres", "-d", "orders_prod", "-v", "ON_ERROR_STOP=1", "-c", "CREATE TABLE orders (id serial primary key, note text); INSERT INTO orders (note) VALUES ('a'),('b')"},
		{"sh", "-c", "pg_dump -U postgres -Fc orders_prod > /tmp/orders.dump"},
	} {
		if out, err := src.exec(args...); err != nil {
			t.Fatalf("%v failed: %v: %s", args, err, out)
		}
	}
	local := filepath.Join(t.TempDir(), "orders.dump")
	if out, err := run("docker", "cp", src.name+":/tmp/orders.dump", local); err != nil {
		t.Fatalf("copying dump out: %v: %s", err, out)
	}

	for _, integrity := range []bool{true, false} {
		cfg := &config.Config{
			Backup:   config.Backup{Source: local, Format: "pg_dump_custom"},
			Postgres: config.Postgres{Image: "postgres:16"},
			Sandbox:  config.Sandbox{ReadyTimeoutDuration: 3 * time.Minute},
			Checks: config.Checks{
				MinTables:        1,
				ArchiveIntegrity: &integrity,
				RowCounts:        []config.RowCount{{Table: "orders", Min: 2}},
			},
		}
		rep, err := Run(cfg)
		if err != nil {
			t.Fatalf("integrity=%v: %v", integrity, err)
		}
		if !rep.Passed {
			t.Fatalf("integrity=%v: expected the drill to pass, got checks: %+v", integrity, rep.Checks)
		}
		if rep.Database != "orders_prod" {
			t.Errorf("integrity=%v: Database = %q, want orders_prod", integrity, rep.Database)
		}
	}
}
