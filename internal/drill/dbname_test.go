package drill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPgDumpDatabaseReadsTheTOCHeader(t *testing.T) {
	toc := ";\n; Archive created at 2026-11-05 10:00:00 UTC\n;     dbname: orders_prod\n;     TOC Entries: 12\n;     Compression: -1\n"
	if got := pgDumpDatabase(toc); got != "orders_prod" {
		t.Errorf("got %q", got)
	}
	if got := pgDumpDatabase("; TOC Entries: 3\n"); got != "" {
		t.Errorf("name invented from a header without one: %q", got)
	}
}

func TestMysqlDumpDatabase(t *testing.T) {
	cases := map[string]string{
		"-- MySQL dump 10.13\n--\n-- Host: localhost    Database: shop\n-- ------\n":                          "shop",
		"-- MySQL dump 10.13\n--\n-- Current Database: `shop_db`\n--\n":                                       "shop_db",
		"-- MySQL dump 10.13\n--\n-- Host: db1.internal    Database: \n-- ------\nCREATE TABLE t (id int);\n": "",
		"CREATE TABLE t (id int);\n": "",
	}
	for body, want := range cases {
		path := filepath.Join(t.TempDir(), "dump.sql")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := mysqlDumpDatabase(path); got != want {
			t.Errorf("mysqlDumpDatabase(%q) = %q, want %q", body, got, want)
		}
	}
	if got := mysqlDumpDatabase(filepath.Join(t.TempDir(), "missing.sql")); got != "" {
		t.Errorf("missing file gave %q", got)
	}
}

func TestDSNDatabase(t *testing.T) {
	cases := map[string]string{
		"postgres://u:pw@h:5432/app?sslmode=require": "app",
		"postgresql://u@h/orders":                    "orders",
		"postgres://u@h":                             "",
		"postgres://u:12#34@h/app":                   "",
		"host=h user=u dbname=app":                   "app",
		"host=h user=u dbname = 'my db'":             "my db",
		"host=h user=u":                              "",
		"host=h dangling":                            "",
	}
	for dsn, want := range cases {
		if got := dsnDatabase(dsn); got != want {
			t.Errorf("dsnDatabase(%q) = %q, want %q", dsn, got, want)
		}
	}
}
