package drill

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

// restoreExistingConnection performs no restore: it connects to an
// already-restored database and verifies it's reachable.
func restoreExistingConnection(cfg *config.Config, rep *report.Report) (*sandbox, error) {
	dsn := cfg.Backup.ConnectionDSN
	target := redactDSN(dsn)
	rep.BackupSource = target
	rep.BackupResolvedKey = target

	if _, err := exec.LookPath("psql"); err != nil {
		return nil, fmt.Errorf("psql not found on PATH (required for backup.format existing_connection): %w", err)
	}

	sb := newExistingConnectionSandbox(dsn)

	out, err := sb.query("SELECT 1")
	res := report.CheckResult{Name: "precheck: target connection reachable", Passed: err == nil && out == "1"}
	switch {
	case err != nil:
		res.Details = firstLine(out)
	case out != "1":
		res.Details = "unexpected response to connectivity probe: " + out
	}
	rep.Checks = append(rep.Checks, res)
	if !res.Passed {
		return sb, fmt.Errorf("connecting to target: %s", res.Details)
	}

	if cfg.Checks.VerifyAsRole != "" {
		roleRes := report.CheckResult{Name: "precheck: verify_as_role exists"}
		exists, err := roleExists(sb, cfg.Checks.VerifyAsRole)
		switch {
		case err != nil:
			roleRes.Details = firstLine(err.Error())
		case !exists:
			roleRes.Details = fmt.Sprintf("role %q not found on the target connection", cfg.Checks.VerifyAsRole)
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

// redactDSN is the one place restoredrill parses connection_dsn: never for
// connection logic, only to strip a password before it reaches a report (and
// therefore notify.webhook_url).
func redactDSN(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "postgres://[redacted: unparseable]"
		}
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
		return u.String()
	}
	var kept []string
	for _, tok := range strings.Fields(dsn) {
		if strings.HasPrefix(strings.ToLower(tok), "password=") {
			continue
		}
		kept = append(kept, tok)
	}
	return strings.Join(kept, " ")
}
