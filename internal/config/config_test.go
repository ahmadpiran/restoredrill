package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restoredrill.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRejectsInvalidBackupFormat(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\n  format: pgdump_custom\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an invalid backup.format")
	}
}

func TestLoadDefaultsBackupFormat(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backup.Format != "pg_dump_custom" {
		t.Errorf("expected default format pg_dump_custom, got %q", cfg.Backup.Format)
	}
}

func TestLoadRequiresBackupSource(t *testing.T) {
	path := writeConfig(t, "backup:\n  format: pg_dump_custom\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error when backup.source is missing")
	}
}

func TestLoadMinSizeBytesDefaultFloor(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checks.MinSizeBytes <= 0 {
		t.Errorf("expected an unset min_size_bytes to get a nonzero default floor, got %d", cfg.Checks.MinSizeBytes)
	}
}

func TestLoadMinSizeBytesExplicitOptOut(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\nchecks:\n  min_size_bytes: -1\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checks.MinSizeBytes != 0 {
		t.Errorf("expected -1 to disable the floor (stored as 0), got %d", cfg.Checks.MinSizeBytes)
	}
}

func TestLoadParsesRPOAndRTOTargets(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\nchecks:\n  rpo_target: 24h\n  rto_target: 30m\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checks.RPOTargetDuration != 24*time.Hour {
		t.Errorf("expected rpo_target parsed as 24h, got %v", cfg.Checks.RPOTargetDuration)
	}
	if cfg.Checks.RTOTargetDuration != 30*time.Minute {
		t.Errorf("expected rto_target parsed as 30m, got %v", cfg.Checks.RTOTargetDuration)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\nchecks:\n  rpo_target: not-a-duration\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an invalid rpo_target duration")
	}
}

func TestLoadRejectsInvalidSandboxKeep(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\nsandbox:\n  keep: sometimes\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an invalid sandbox.keep")
	}
}

func TestLoadRequiresS3ObjectPatternForUnsniffableFormatWithPrefix(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: s3://bucket/prefix/\n  format: pg_dump_sql\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error: pg_dump_sql has no content signature and needs s3_object_pattern with a prefix source")
	}
}

func TestLoadAllowsUnsniffableFormatWithPrefixIfPatternSet(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: s3://bucket/prefix/\n  format: pg_dump_sql\n  s3_object_pattern: \"*.sql\"\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("expected no error once s3_object_pattern is set, got %v", err)
	}
}

func TestLoadDoesNotRequireS3ObjectPatternForSniffableFormat(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: s3://bucket/prefix/\n  format: pg_dump_custom\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("expected no error: pg_dump_custom is verifiable by content, got %v", err)
	}
}

func TestLoadDoesNotRequireS3ObjectPatternForNonPrefixSource(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: s3://bucket/prefix/exact.dump\n  format: pg_dump_sql\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("expected no error: an exact key isn't ambiguous, got %v", err)
	}
}

func TestLoadRejectsInvalidS3ObjectPattern(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\n  s3_object_pattern: \"[\"\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for a malformed s3_object_pattern glob")
	}
}

func TestLoadRejectsVerifyAsRoleWithoutGlobalsSource(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\nchecks:\n  verify_as_role: app_user\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error: checks.verify_as_role requires backup.globals_source")
	}
}

func TestLoadAllowsVerifyAsRoleWithGlobalsSource(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\n  globals_source: /tmp/globals.sql\nchecks:\n  verify_as_role: app_user\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("expected no error once backup.globals_source is set, got %v", err)
	}
}

func TestLoadAllowsGlobalsSourceAloneWithoutVerifyAsRole(t *testing.T) {
	path := writeConfig(t, "backup:\n  source: /tmp/x.dump\n  globals_source: /tmp/globals.sql\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("expected no error: globals_source alone is valid, got %v", err)
	}
}

func TestLoadDoesNotRequireBackupSourceForPgbackrest(t *testing.T) {
	path := writeConfig(t, "backup:\n  format: pgbackrest\n  pgbackrest_config: /tmp/pgbackrest.conf\n  pgbackrest_stanza: main\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("expected no error: pgbackrest has no single backup.source file, got %v", err)
	}
}

func TestLoadRequiresPgbackrestConfig(t *testing.T) {
	path := writeConfig(t, "backup:\n  format: pgbackrest\n  pgbackrest_stanza: main\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error when backup.pgbackrest_config is missing")
	}
}

func TestLoadRequiresPgbackrestStanza(t *testing.T) {
	path := writeConfig(t, "backup:\n  format: pgbackrest\n  pgbackrest_config: /tmp/pgbackrest.conf\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error when backup.pgbackrest_stanza is missing")
	}
}
