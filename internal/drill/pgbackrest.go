package drill

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

// A local-repository pgbackrest.conf's repo1-path must equal
// pgbackrestRepoPath for the mount below to line up.
const (
	pgbackrestConfigPath = "/etc/pgbackrest/pgbackrest.conf"
	pgbackrestRepoPath   = "/var/lib/pgbackrest"
)

// restorePgbackrest returns the sandbox on error too, once one exists, so
// the caller can still clean it up.
func restorePgbackrest(cfg *config.Config, rep *report.Report) (*sandbox, error) {
	mounts := []mount{{hostPath: cfg.Backup.PgbackrestConfig, containerPath: pgbackrestConfigPath}}
	if cfg.Backup.PgbackrestRepoPath != "" {
		mounts = append(mounts, mount{hostPath: cfg.Backup.PgbackrestRepoPath, containerPath: pgbackrestRepoPath})
	}

	sb := newSandbox(cfg.Postgres.Image, cfg.Sandbox.ReadyTimeoutDuration, mounts)
	if err := sb.startUninitialized(); err != nil {
		return sb, err
	}

	backup, err := pgbackrestLatestBackup(sb, cfg.Backup.PgbackrestStanza)
	if err != nil {
		return sb, fmt.Errorf("pgbackrest info: %w", err)
	}
	rep.BackupResolvedKey = backup.label
	if !backup.timestamp.IsZero() {
		rep.BackupTimestamp = report.At(backup.timestamp)
		rep.BackupAgeSeconds = time.Since(backup.timestamp).Seconds()
	}

	if cfg.Checks.RPOTargetDuration > 0 {
		rep.RPOTargetSeconds = cfg.Checks.RPOTargetDuration.Seconds()
		met := !backup.timestamp.IsZero() && time.Since(backup.timestamp) <= cfg.Checks.RPOTargetDuration
		rep.RPOMet = &met
		res := report.CheckResult{Name: fmt.Sprintf("precheck: RPO target met (backup fresher than %s)", cfg.Checks.RPOTargetDuration)}
		if backup.timestamp.IsZero() {
			res.Details = "backup timestamp could not be determined"
		} else {
			res.Details = fmt.Sprintf("backup is %s old", time.Since(backup.timestamp).Round(time.Second))
		}
		res.Passed = met
		rep.Checks = append(rep.Checks, res)
		if !met {
			return sb, fmt.Errorf("RPO target check failed: %s", res.Details)
		}
	}

	rep.RestoreStartedAt = report.Now()
	restoreStart := time.Now()

	if out, err := sb.execAsUser("postgres", "pgbackrest", "--stanza="+cfg.Backup.PgbackrestStanza, "restore"); err != nil {
		return sb, fmt.Errorf("pgbackrest restore failed: %v: %s", err, firstLine(out))
	}
	if err := sb.startPostgres(); err != nil {
		return sb, err
	}
	if err := sb.waitForRecoveryComplete(); err != nil {
		return sb, err
	}

	rep.RestoreDurationSeconds = time.Since(restoreStart).Seconds()
	rep.RestoreFinishedAt = report.Now()

	if cfg.Checks.VerifyAsRole != "" {
		roleRes := report.CheckResult{Name: "precheck: verify_as_role exists"}
		exists, err := roleExists(sb, cfg.Checks.VerifyAsRole)
		switch {
		case err != nil:
			roleRes.Details = firstLine(err.Error())
		case !exists:
			roleRes.Details = fmt.Sprintf("role %q not found in the restored database", cfg.Checks.VerifyAsRole)
		default:
			roleRes.Passed = true
		}
		rep.Checks = append(rep.Checks, roleRes)
		if !roleRes.Passed {
			return sb, fmt.Errorf("verify_as_role check failed: %s", roleRes.Details)
		}
	}

	return sb, nil
}

type pgbackrestBackup struct {
	label     string
	timestamp time.Time
}

type pgbackrestInfoStanza struct {
	Backup []struct {
		Label     string `json:"label"`
		Timestamp struct {
			Stop int64 `json:"stop"`
		} `json:"timestamp"`
	} `json:"backup"`
}

// pgbackrest lists a stanza's backups oldest first.
func pgbackrestLatestBackup(sb *sandbox, stanza string) (pgbackrestBackup, error) {
	out, err := sb.execAsUser("postgres", "pgbackrest", "--stanza="+stanza, "info", "--output=json")
	if err != nil {
		return pgbackrestBackup{}, fmt.Errorf("%v: %s", err, firstLine(out))
	}
	var stanzas []pgbackrestInfoStanza
	if err := json.Unmarshal([]byte(out), &stanzas); err != nil {
		return pgbackrestBackup{}, fmt.Errorf("parsing pgbackrest info output: %w", err)
	}
	if len(stanzas) == 0 || len(stanzas[0].Backup) == 0 {
		return pgbackrestBackup{}, fmt.Errorf("stanza %q has no backups", stanza)
	}
	latest := stanzas[0].Backup[len(stanzas[0].Backup)-1]
	return pgbackrestBackup{label: latest.Label, timestamp: time.Unix(latest.Timestamp.Stop, 0)}, nil
}
