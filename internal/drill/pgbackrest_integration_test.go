package drill

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
)

const pgbackrestTestImage = "restoredrill-test-pgbackrest"

// Real-Postgres, real-pgbackrest integration test: produces a real
// stanza/backup, then runs Run() against it and confirms a row that only
// exists in WAL archived after the backup made it through. Skips if docker
// isn't available.
func TestPgbackrestRestore(t *testing.T) {
	if out, err := run("docker", "info"); err != nil {
		t.Skipf("docker not available, skipping integration test: %v: %s", err, firstLine(out))
	}
	if out, err := run("docker", "build", "-t", pgbackrestTestImage, "testdata/pgbackrest"); err != nil {
		t.Fatalf("building pgbackrest test image: %v: %s", err, out)
	}

	repoDir := t.TempDir()
	confPath := filepath.Join(t.TempDir(), "pgbackrest.conf")
	confBody := "[main]\npg1-path=/var/lib/postgresql/data\n\n[global]\nrepo1-path=" + pgbackrestRepoPath + "\nstart-fast=y\n"
	if err := os.WriteFile(confPath, []byte(confBody), 0o644); err != nil {
		t.Fatalf("writing pgbackrest.conf: %v", err)
	}

	src := newSandbox(pgbackrestTestImage, 2*time.Minute, []mount{
		{hostPath: repoDir, containerPath: pgbackrestRepoPath},
		{hostPath: confPath, containerPath: pgbackrestConfigPath},
	})
	if out, err := run("docker", "run", "-d", "--name", src.name,
		"-v", repoDir+":"+pgbackrestRepoPath, "-v", confPath+":"+pgbackrestConfigPath,
		"-e", "POSTGRES_PASSWORD=restoredrill", pgbackrestTestImage,
		"postgres", "-c", "archive_mode=on",
		"-c", "archive_command=pgbackrest --stanza=main archive-push %p",
		"-c", "wal_level=replica"); err != nil {
		t.Fatalf("starting source container: %v: %s", err, out)
	}
	src.created = true
	t.Cleanup(func() {
		if err := src.destroy(); err != nil {
			t.Logf("warning: failed to destroy source container %s: %v", src.name, err)
		}
	})

	deadline := time.Now().Add(src.readyTimeout)
	for {
		if _, err := src.exec("pg_isready", "-U", "postgres"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("source postgres not ready after %s", src.readyTimeout)
		}
		time.Sleep(time.Second)
	}

	execSQL := func(sql string) {
		t.Helper()
		if out, err := src.execAsUser("postgres", "psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", sql); err != nil {
			t.Fatalf("setup SQL %q failed: %v: %s", sql, err, out)
		}
	}
	execPgbackrest := func(args ...string) {
		t.Helper()
		if out, err := src.execAsUser("postgres", append([]string{"pgbackrest", "--stanza=main"}, args...)...); err != nil {
			t.Fatalf("pgbackrest %v failed: %v: %s", args, err, out)
		}
	}

	execPgbackrest("stanza-create")
	execSQL("CREATE TABLE pgbackrest_probe (id serial primary key, note text)")
	execSQL("INSERT INTO pgbackrest_probe (note) VALUES ('before-backup')")
	execPgbackrest("backup", "--type=full")
	execSQL("INSERT INTO pgbackrest_probe (note) VALUES ('after-backup-wal-replay')")
	execSQL("SELECT pg_switch_wal()")
	time.Sleep(2 * time.Second) // let the switched segment finish archiving

	cfg := &config.Config{
		Backup: config.Backup{
			Format:             "pgbackrest",
			PgbackrestConfig:   confPath,
			PgbackrestStanza:   "main",
			PgbackrestRepoPath: repoDir,
		},
		Postgres: config.Postgres{Image: pgbackrestTestImage},
		Sandbox:  config.Sandbox{ReadyTimeoutDuration: 2 * time.Minute},
		Checks: config.Checks{
			MinTables: 1,
			Queries: []config.Assertion{
				{Name: "both rows present", SQL: "SELECT count(*) = 2 FROM pgbackrest_probe"},
			},
		},
	}

	rep, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run failed: %v (report: %+v)", err, rep)
	}
	if !rep.Passed {
		t.Fatalf("expected the drill to pass, got checks: %+v", rep.Checks)
	}
	if rep.BackupResolvedKey == "" {
		t.Error("expected BackupResolvedKey to be set to the pgbackrest backup label")
	}
	if rep.BackupTimestamp.IsZero() {
		t.Error("expected BackupTimestamp to be set from pgbackrest info")
	}
	if rep.RestoreDurationSeconds <= 0 {
		t.Error("expected a positive RestoreDurationSeconds")
	}
}
