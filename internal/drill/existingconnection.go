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
	rep.Database = dsnDatabase(dsn)

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

func redactDSN(dsn string) string {
	if isURIDSN(dsn) {
		u, ok := parseURIDSN(dsn)
		if !ok {
			return "postgres://[redacted: unparseable]"
		}
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
		u.RawQuery = dropSecretParams(u.RawQuery)
		return u.String()
	}
	pairs, ok := parseKeywordDSN(dsn)
	if !ok {
		return "[redacted: unparseable]"
	}
	var kept []string
	for _, p := range pairs {
		if !isSecretKey(p.key) {
			kept = append(kept, p.text)
		}
	}
	return strings.Join(kept, " ")
}

func splitDSNPassword(dsn string) (clean, password string) {
	if isURIDSN(dsn) {
		u, ok := parseURIDSN(dsn)
		if !ok {
			return dsn, ""
		}
		if u.User != nil {
			if pw, set := u.User.Password(); set {
				password = pw
			}
			u.User = url.User(u.User.Username())
		}
		var kept []string
		for _, pair := range strings.Split(u.RawQuery, "&") {
			key, val, _ := strings.Cut(pair, "=")
			if k, err := url.QueryUnescape(key); err == nil && k == "password" {
				if v, err := url.QueryUnescape(val); err == nil {
					password = v
					continue
				}
			}
			kept = append(kept, pair)
		}
		u.RawQuery = strings.Join(kept, "&")
		return u.String(), password
	}
	pairs, ok := parseKeywordDSN(dsn)
	if !ok {
		return dsn, ""
	}
	var kept []string
	for _, p := range pairs {
		if p.key == "password" {
			password = p.value
			continue
		}
		kept = append(kept, p.text)
	}
	return strings.Join(kept, " "), password
}

func isURIDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

func parseURIDSN(dsn string) (*url.URL, bool) {
	u, err := url.Parse(dsn)
	// A raw "?", "#" or "/" in a password shifts part of it into the host, query or fragment.
	if err != nil || u.Fragment != "" || (u.User == nil && strings.Contains(dsn, "@")) {
		return nil, false
	}
	return u, true
}

func isSecretKey(k string) bool {
	k = strings.ToLower(k)
	return k == "password" || k == "sslpassword"
}

// Not url.Values: that would reorder and re-escape everything else.
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

type dsnPair struct {
	key, text, value string
}

func parseKeywordDSN(dsn string) (pairs []dsnPair, ok bool) {
	i := 0
	for {
		for i < len(dsn) && isSpace(dsn[i]) {
			i++
		}
		if i == len(dsn) {
			return pairs, true
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
			return nil, false
		}
		i++
		for i < len(dsn) && isSpace(dsn[i]) {
			i++
		}
		var value strings.Builder
		if i < len(dsn) && dsn[i] == '\'' {
			i++
			for i < len(dsn) && dsn[i] != '\'' {
				if dsn[i] == '\\' {
					i++
				}
				if i < len(dsn) {
					value.WriteByte(dsn[i])
				}
				i++
			}
			if i >= len(dsn) {
				return nil, false
			}
			i++
		} else {
			for i < len(dsn) && !isSpace(dsn[i]) {
				if dsn[i] == '\\' {
					i++
				}
				if i < len(dsn) {
					value.WriteByte(dsn[i])
				}
				i++
			}
		}
		if i > len(dsn) {
			return nil, false
		}
		pairs = append(pairs, dsnPair{key: key, text: dsn[start:i], value: value.String()})
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
