package drill

import (
	"strings"
	"testing"

	"github.com/ahmadpiran/restoredrill/internal/backupformat"
	"github.com/ahmadpiran/restoredrill/internal/config"
)

func TestEveryKnownFormatHasAnEngine(t *testing.T) {
	for _, name := range backupformat.Names() {
		if _, ok := engines[name]; !ok {
			t.Errorf("backup format %q has no engine entry", name)
		}
	}
}

func TestEngineForFormat(t *testing.T) {
	if engineFor("mysqldump_sql").parseBool == nil {
		t.Fatal("expected mysqldump_sql to resolve to an engine")
	}
	if _, ok := engineFor("mysqldump_sql").parseBool("1"); !ok {
		t.Error("expected mysqldump_sql to resolve to the MySQL engine")
	}
	if _, ok := engineFor("pg_dump_custom").parseBool("t"); !ok {
		t.Error("expected pg_dump_custom to resolve to the Postgres engine")
	}
}

func TestQuoteIdentMySQL(t *testing.T) {
	cases := map[string]string{
		"users":       "`users`",
		"appdb.users": "`appdb`.`users`",
		"weird`table": "`weird``table`",
	}
	for in, want := range cases {
		if got := quoteIdentMySQL(in); got != want {
			t.Errorf("quoteIdentMySQL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseBool(t *testing.T) {
	cases := []struct {
		engine  string
		parse   func(string) (bool, bool)
		in      string
		wantVal bool
		wantOK  bool
	}{
		{"postgres", parseBoolPostgres, "t", true, true},
		{"postgres", parseBoolPostgres, "true", true, true},
		{"postgres", parseBoolPostgres, "f", false, true},
		{"postgres", parseBoolPostgres, "false", false, true},
		{"postgres", parseBoolPostgres, "1", false, false},
		{"postgres", parseBoolPostgres, "42", false, false},
		{"mysql", parseBoolMySQL, "1", true, true},
		{"mysql", parseBoolMySQL, "0", false, true},
		{"mysql", parseBoolMySQL, "t", false, false},
		{"mysql", parseBoolMySQL, "NULL", false, false},
		{"mysql", parseBoolMySQL, "42", false, false},
	}
	for _, tc := range cases {
		val, ok := tc.parse(tc.in)
		if val != tc.wantVal || ok != tc.wantOK {
			t.Errorf("%s parseBool(%q) = (%v, %v), want (%v, %v)", tc.engine, tc.in, val, ok, tc.wantVal, tc.wantOK)
		}
	}
}

func TestEvaluateAssertionMySQLBooleans(t *testing.T) {
	if res := evaluateAssertion(mysqlEngine, "c", "1", nil); !res.Passed {
		t.Errorf("expected 1 to pass for MySQL, got %q", res.Details)
	}
	if res := evaluateAssertion(mysqlEngine, "c", "0", nil); res.Passed {
		t.Error("expected 0 to fail for MySQL")
	}
	if res := evaluateAssertion(mysqlEngine, "c", "t", nil); res.Passed {
		t.Error("expected a Postgres true (t) not to be accepted as one by MySQL")
	}
}

func TestMinTablesSQLScopesToTheConfiguredDatabase(t *testing.T) {
	sql := mysqlEngine.minTablesSQL("app'db")
	if !strings.Contains(sql, "'app''db'") {
		t.Errorf("expected the database name to be quoted as a literal, got %q", sql)
	}
	if !strings.Contains(sql, "BASE TABLE") {
		t.Errorf("expected views to be excluded from the table count, got %q", sql)
	}
}

func TestQueryMySQLRefusesVerifyAsRole(t *testing.T) {
	sb := &sandbox{name: "x", eng: mysqlEngine, database: "appdb"}
	if _, err := sb.queryAs("app_user", "SELECT 1"); err == nil {
		t.Fatal("expected queryAs with a role to fail for MySQL rather than silently run with full privileges")
	}
}

func TestBrokenDefinersSQLCoversEveryDefinerBearingObject(t *testing.T) {
	sql := brokenDefinersSQL("appdb")
	for _, table := range []string{"information_schema.VIEWS", "information_schema.TRIGGERS", "information_schema.EVENTS", "information_schema.ROUTINES"} {
		if !strings.Contains(sql, table) {
			t.Errorf("expected %s to be checked for missing definers", table)
		}
	}
	if !strings.Contains(sql, "mysql.user") {
		t.Error("expected definers to be compared against mysql.user")
	}
}

func TestBrokenDefinersSQLScopesToTheConfiguredDatabase(t *testing.T) {
	sql := brokenDefinersSQL("app'db")
	if !strings.Contains(sql, "o.s = 'app''db'") {
		t.Errorf("expected the database name to be quoted as a literal and scoped on, got %q", sql)
	}
}

func TestDefinerIntegrityIsExpectedAfterAnAbortedDrill(t *testing.T) {
	names := expectedCheckNames(config.Checks{DefinerIntegrity: true})
	for _, n := range names {
		if n == "definer integrity" {
			return
		}
	}
	t.Errorf("expected an aborted drill to record the definer integrity check as unevaluated, got %v", names)
}
