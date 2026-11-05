package drill

import (
	"io"
	"os"
	"regexp"
	"strings"
)

var (
	pgDumpDBName = regexp.MustCompile(`(?m)^;\s+dbname:\s*(\S.*?)\s*$`)
	mysqlDumpDB  = regexp.MustCompile("(?m)^-- (?:Host: .*\\sDatabase|Current Database): *`?([^`\\s]+)`?\\s*$")
)

// pgDumpDatabase reads the database name from the header of `pg_restore --list`.
func pgDumpDatabase(toc string) string {
	if m := pgDumpDBName.FindStringSubmatch(toc); m != nil {
		return m[1]
	}
	return ""
}

// mysqlDumpDatabase reads the database name from the first lines of a
// mysqldump file. It returns "" when the dump does not record one.
func mysqlDumpDatabase(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, 16<<10))
	if m := mysqlDumpDB.FindStringSubmatch(string(head)); m != nil {
		return m[1]
	}
	return ""
}

// dsnDatabase returns the database named in a connection string, or "" when it
// does not name one (libpq then falls back to the user name).
func dsnDatabase(dsn string) string {
	if isURIDSN(dsn) {
		u, ok := parseURIDSN(dsn)
		if !ok {
			return ""
		}
		return strings.TrimPrefix(u.Path, "/")
	}
	pairs, ok := parseKeywordDSN(dsn)
	if !ok {
		return ""
	}
	for _, p := range pairs {
		if p.key == "dbname" {
			return p.value
		}
	}
	return ""
}
