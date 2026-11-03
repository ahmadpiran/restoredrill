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
		// A raw "?", "#" or "/" in a password makes the parser read part of it
		// as the host, query or fragment. libpq URIs never carry a fragment.
		if err != nil || u.Fragment != "" || (u.User == nil && strings.Contains(dsn, "@")) {
			return "postgres://[redacted: unparseable]"
		}
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
		u.RawQuery = dropSecretParams(u.RawQuery)
		return u.String()
	}
	out, ok := redactKeywordDSN(dsn)
	if !ok {
		return "[redacted: unparseable]"
	}
	return out
}

func isSecretKey(k string) bool {
	k = strings.ToLower(k)
	return k == "password" || k == "sslpassword"
}

// dropSecretParams removes secret parameters from a URI query, keeping the
// order and escaping of everything else.
func dropSecretParams(query string) string {
	var kept []string
	for _, pair := range strings.Split(query, "&") {
		key, _, _ := strings.Cut(pair, "=")
		if k, err := url.QueryUnescape(key); err == nil && isSecretKey(k) {
			continue
		}
		kept = append(kept, pair)
	}
	return strings.Join(kept, "&")
}

// redactKeywordDSN drops password and sslpassword from a libpq "key=value"
// string. It follows libpq's rules: spaces are allowed around "=", and a value
// may be single-quoted with backslash escapes. ok is false when the string
// does not parse, in which case nothing of it should be shown.
func redactKeywordDSN(dsn string) (out string, ok bool) {
	var kept []string
	i := 0
	for {
		for i < len(dsn) && isSpace(dsn[i]) {
			i++
		}
		if i == len(dsn) {
			return strings.Join(kept, " "), true
		}
		start := i
		for i < len(dsn) && dsn[i] != '=' && !isSpace(dsn[i]) {
			i++
		}
		key := dsn[start:i]
		for i < len(dsn) && isSpace(dsn[i]) {
			i++
		}
		if key == "" || i == len(dsn) || dsn[i] != '=' {
			return "", false
		}
		i++
		for i < len(dsn) && isSpace(dsn[i]) {
			i++
		}
		if i < len(dsn) && dsn[i] == '\'' {
			i++
			for i < len(dsn) && dsn[i] != '\'' {
				if dsn[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(dsn) {
				return "", false
			}
			i++
		} else {
			for i < len(dsn) && !isSpace(dsn[i]) {
				if dsn[i] == '\\' {
					i++
				}
				i++
			}
		}
		if i > len(dsn) {
			return "", false
		}
		if !isSecretKey(key) {
			kept = append(kept, dsn[start:i])
		}
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
