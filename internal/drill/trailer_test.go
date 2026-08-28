package drill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

func TestPlainSQLMissingTrailerFailsBeforeSandbox(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup.sql")
	if err := os.WriteFile(path, []byte("COPY public.t (id) FROM stdin;\n1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Backup: config.Backup{Source: path, Format: "pg_dump_sql"},
		Checks: config.Checks{MinTables: 5},
	}

	rep, err := Run(cfg)
	if err == nil {
		t.Fatal("expected Run to fail on a truncated plain-SQL dump")
	}
	if rep.Passed {
		t.Error("expected Passed=false")
	}

	byName := map[string]report.CheckResult{}
	for _, c := range rep.Checks {
		byName[c.Name] = c
	}

	trailer, ok := byName["precheck: dump file complete (trailer present)"]
	if !ok {
		t.Fatal("expected the trailer precheck to be recorded")
	}
	if trailer.Passed {
		t.Error("expected the trailer precheck to fail")
	}

	tables, ok := byName["at least 5 tables restored"]
	if !ok {
		t.Fatal("expected the unreached downstream check to be recorded")
	}
	if tables.Passed {
		t.Error("expected the unreached downstream check to be marked failed")
	}
}
