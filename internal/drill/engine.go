package drill

import (
	"fmt"
	"os"
	"strings"
)

type engine struct {
	quoteIdent   func(string) string
	minTablesSQL func(database string) string
	parseBool    func(string) (val bool, ok bool)
	query        func(sb *sandbox, role, sql string) (string, error)
}

const mysqlRootPassword = "restoredrill"

func mysqlEnv() []string { return []string{"MYSQL_PWD=" + mysqlRootPassword} }

var engines = map[string]engine{
	"pg_dump_custom":      postgresEngine,
	"pg_dump_sql":         postgresEngine,
	"pgbackrest":          postgresEngine,
	"existing_connection": postgresEngine,
	"mysqldump_sql":       mysqlEngine,
}

func engineFor(format string) engine {
	if e, ok := engines[format]; ok {
		return e
	}
	return postgresEngine
}

var postgresEngine = engine{
	quoteIdent: quoteIdent,
	minTablesSQL: func(string) string {
		return "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')"
	},
	parseBool: parseBoolPostgres,
	query:     queryPostgres,
}

var mysqlEngine = engine{
	quoteIdent: quoteIdentMySQL,
	minTablesSQL: func(database string) string {
		return "SELECT count(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = " +
			quoteLiteral(database) + " AND TABLE_TYPE = 'BASE TABLE'"
	},
	parseBool: parseBoolMySQL,
	query:     queryMySQL,
}

func parseBoolPostgres(s string) (bool, bool) {
	switch s {
	case "t", "true":
		return true, true
	case "f", "false":
		return false, true
	}
	return false, false
}

func parseBoolMySQL(s string) (bool, bool) {
	switch s {
	case "1":
		return true, true
	case "0":
		return false, true
	}
	return false, false
}

func quoteIdentMySQL(ident string) string {
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		parts[i] = "`" + strings.ReplaceAll(p, "`", "``") + "`"
	}
	return strings.Join(parts, ".")
}

// queryPostgres prefixes sql with "SET ROLE role" so it runs as role instead
// of postgres (a superuser can SET ROLE without a membership check). -q
// suppresses the "SET" status line psql would otherwise print ahead of the
// result, even under -t.
func queryPostgres(sb *sandbox, role, sql string) (string, error) {
	var args []string
	if sb.dsn == "" {
		args = []string{"-U", "postgres", "-d", "postgres", "-t", "-A"}
	} else {
		args = []string{"-d", sb.dsn, "-t", "-A"}
	}
	if role != "" {
		args = append(args, "-q")
		sql = fmt.Sprintf("SET ROLE %s; %s", quoteSingleIdent(role), sql)
	}
	args = append(args, "-c", sql)
	return sb.runPsql(args...)
}

// queryMySQL passes the password as MYSQL_PWD because -p writes a warning to
// stderr, which run() merges into the result.
func queryMySQL(sb *sandbox, role, sql string) (string, error) {
	if role != "" {
		return "", fmt.Errorf("verify_as_role is not supported for MySQL: SET ROLE does not drop the connecting account's own privileges")
	}
	return sb.execEnv(mysqlEnv(), "mysql", "-u", "root", "-D", sb.database, "-N", "-B", "-e", sql)
}

// runPsql runs psql inside the sandbox container, or, when sb.dsn is set
// (existing_connection), directly on the host against the target. The
// host path enforces a connect timeout and a read-only session:
// restoredrill is a guest on that database, never the owner.
func (s *sandbox) runPsql(args ...string) (string, error) {
	if s.dsn == "" {
		return s.exec(append([]string{"psql"}, args...)...)
	}
	env := append(os.Environ(),
		"PGCONNECT_TIMEOUT=10",
		"PGOPTIONS=-c default_transaction_read_only=on",
	)
	return runEnv(env, append([]string{"psql"}, args...)...)
}
