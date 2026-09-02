package drill

import (
	"fmt"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

func restoreMysqlDump(cfg *config.Config, rep *report.Report) (*sandbox, error) {
	fr, err := fetch(cfg.Backup)
	if err != nil {
		return nil, fmt.Errorf("fetching backup: %w", err)
	}
	if fr.cleanup != nil {
		defer fr.cleanup()
	}
	rep.BackupResolvedKey = fr.resolvedKey
	for _, c := range fr.candidates {
		rep.BackupCandidatesConsidered = append(rep.BackupCandidatesConsidered, report.BackupCandidate{Name: c.Name, Reason: c.Reason})
	}

	if err := precheckBackupFile(cfg, rep, fr); err != nil {
		return nil, err
	}

	sb := newMySQLSandbox(cfg.MySQL.Image, cfg.MySQL.Database, cfg.Sandbox.ReadyTimeoutDuration)
	if err := sb.startMySQL(); err != nil {
		return sb, err
	}

	const remote = "/tmp/restoredrill-backup"
	if err := sb.copyIn(fr.localPath, remote); err != nil {
		return sb, err
	}

	rep.RestoreStartedAt = report.Now()
	restoreStart := time.Now()
	if err := restoreMysql(sb, remote); err != nil {
		return sb, fmt.Errorf("restore failed: %w", err)
	}
	rep.RestoreDurationSeconds = time.Since(restoreStart).Seconds()
	rep.RestoreFinishedAt = report.Now()

	return sb, nil
}

// The mysql client's "source" stops at the first error and exits nonzero,
// matching psql's ON_ERROR_STOP.
func restoreMysql(sb *sandbox, remote string) error {
	if out, err := sb.execEnv(mysqlEnv(), "mysql", "-u", "root",
		"-e", "CREATE DATABASE IF NOT EXISTS "+quoteIdentMySQL(sb.database)); err != nil {
		return fmt.Errorf("creating target database: %v: %s", err, firstLine(out))
	}
	if out, err := sb.execEnv(mysqlEnv(), "mysql", "-u", "root", "-D", sb.database,
		"-e", "source "+remote); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}
