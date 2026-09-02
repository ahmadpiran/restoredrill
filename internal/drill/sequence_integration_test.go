package drill

import (
	"strings"
	"testing"
	"time"
)

// Real-Postgres integration test: starts a sandbox, verifies
// brokenSequencesSQL on both a healthy and a forced-broken sequence. Skips
// if docker isn't available.
func TestSequenceIntegrityCheck(t *testing.T) {
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

	execSQL("CREATE TABLE widgets (id serial primary key, name text)")
	execSQL("INSERT INTO widgets (name) VALUES ('a'), ('b'), ('c')")

	out, err := sb.query(brokenSequencesSQL)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if out != "" {
		t.Fatalf("expected no broken sequences after normal inserts, got %q", out)
	}

	// Force the sequence behind the column's actual max.
	execSQL("SELECT setval('widgets_id_seq', 1, true)")

	out, err = sb.query(brokenSequencesSQL)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if !strings.Contains(out, "widgets_id_seq") {
		t.Fatalf("expected widgets_id_seq to be reported as broken, got %q", out)
	}
}
