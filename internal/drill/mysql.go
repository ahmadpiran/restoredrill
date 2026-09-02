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

// A restored object whose DEFINER account is missing (mysqldump does not
// dump users) loads cleanly and only fails when invoked, with ERROR 1449.
// group_concat would otherwise truncate the list at 1KB.
func brokenDefinersSQL(database string) string {
	db := quoteLiteral(database)
	return `SET SESSION group_concat_max_len = 100000;
SELECT coalesce(group_concat(obj ORDER BY obj SEPARATOR ', '), '') FROM (
  SELECT concat('view ', TABLE_SCHEMA, '.', TABLE_NAME) AS obj, DEFINER AS d, TABLE_SCHEMA AS s FROM information_schema.VIEWS
  UNION ALL SELECT concat('trigger ', TRIGGER_SCHEMA, '.', TRIGGER_NAME), DEFINER, TRIGGER_SCHEMA FROM information_schema.TRIGGERS
  UNION ALL SELECT concat('event ', EVENT_SCHEMA, '.', EVENT_NAME), DEFINER, EVENT_SCHEMA FROM information_schema.EVENTS
  UNION ALL SELECT concat('routine ', ROUTINE_SCHEMA, '.', ROUTINE_NAME), DEFINER, ROUTINE_SCHEMA FROM information_schema.ROUTINES
) o WHERE o.s = ` + db + `
  AND o.d NOT IN (SELECT concat(user, '@', host) FROM mysql.user)`
}
