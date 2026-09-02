package backupformat

import "testing"

func TestValid(t *testing.T) {
	if !Valid("pg_dump_custom") {
		t.Error("expected pg_dump_custom to be valid")
	}
	if !Valid("pg_dump_sql") {
		t.Error("expected pg_dump_sql to be valid")
	}
	if Valid("mongodump") {
		t.Error("expected an unknown format to be invalid")
	}
}

func TestSniffable(t *testing.T) {
	if !Sniffable("pg_dump_custom") {
		t.Error("expected pg_dump_custom to be sniffable (PGDMP header)")
	}
	if Sniffable("pg_dump_sql") {
		t.Error("expected pg_dump_sql to be unsniffable (plain SQL has no header)")
	}
	if Sniffable("mongodump") {
		t.Error("expected an unknown format to be unsniffable")
	}
}

func TestMatchesPgDumpCustom(t *testing.T) {
	if !Matches("pg_dump_custom", []byte("PGDMP\x01\x0e\x00")) {
		t.Error("expected a real PGDMP header to match")
	}
	if Matches("pg_dump_custom", []byte("not a dump at all")) {
		t.Error("expected non-dump content not to match")
	}
	if Matches("pg_dump_custom", []byte("")) {
		t.Error("expected empty content not to match")
	}
}

func TestMatchesUnsniffableFormatAlwaysFalse(t *testing.T) {
	if Matches("pg_dump_sql", []byte("PGDMP")) {
		t.Error("expected Matches to return false for an unsniffable format, regardless of content")
	}
}

func TestTrailerable(t *testing.T) {
	if Trailerable("pg_dump_custom") {
		t.Error("expected pg_dump_custom to be untrailerable (has its own TOC check)")
	}
	if !Trailerable("pg_dump_sql") {
		t.Error("expected pg_dump_sql to be trailerable")
	}
	if !Trailerable("mysqldump_sql") {
		t.Error("expected mysqldump_sql to be trailerable")
	}
	if Trailerable("mongodump") {
		t.Error("expected an unknown format to be untrailerable")
	}
}

const realPgDumpSQLTail = `
COPY public.t (id) FROM stdin;
1
\.


--
-- PostgreSQL database dump complete
--

\unrestrict 9XvOA8PlNswAqIrk1QzL28jR2GSMpmeHoLcKEO78pGLAubSE6BapqImO2sn6tg0

`

func TestCompletePgDumpSQL(t *testing.T) {
	if !Complete("pg_dump_sql", []byte(realPgDumpSQLTail)) {
		t.Error("expected a real pg_dump plain-SQL trailer to be complete")
	}
	truncated := "COPY public.t (id) FROM stdin;\n1\n2\n3\n4\n5\n"
	if Complete("pg_dump_sql", []byte(truncated)) {
		t.Error("expected a truncated dump (no trailer) to be incomplete")
	}
	if Complete("pg_dump_sql", []byte("")) {
		t.Error("expected an empty tail to be incomplete")
	}
}

func TestCompleteUnsupportedFormatAlwaysFalse(t *testing.T) {
	if Complete("pg_dump_custom", []byte(realPgDumpSQLTail)) {
		t.Error("expected Complete to return false for a format with no trailer check")
	}
	if Complete("mongodump", []byte(realPgDumpSQLTail)) {
		t.Error("expected Complete to return false for an unknown format")
	}
}

func TestSniffableMysqldumpSQL(t *testing.T) {
	if Sniffable("mysqldump_sql") {
		t.Error("expected mysqldump_sql to be unsniffable (--skip-comments strips its header)")
	}
}

const realMysqldumpSQLTail = `
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- Dump completed on 2026-09-02 17:15:21
`

const undatedMysqldumpSQLTail = `
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- Dump completed
`

func TestCompleteMysqldumpSQL(t *testing.T) {
	if !Complete("mysqldump_sql", []byte(realMysqldumpSQLTail)) {
		t.Error("expected a real mysqldump trailer to be complete")
	}
	if !Complete("mysqldump_sql", []byte(undatedMysqldumpSQLTail)) {
		t.Error("expected an undated mysqldump trailer to be complete")
	}
	truncated := "INSERT INTO `t` VALUES (1),(2),(3);"
	if Complete("mysqldump_sql", []byte(truncated)) {
		t.Error("expected a truncated dump (no trailer) to be incomplete")
	}
	if Complete("mysqldump_sql", []byte("")) {
		t.Error("expected an empty tail to be incomplete")
	}
}

func TestTrailersDoNotCrossFormats(t *testing.T) {
	if Complete("pg_dump_sql", []byte(realMysqldumpSQLTail)) {
		t.Error("expected a mysqldump trailer not to satisfy pg_dump_sql")
	}
	if Complete("mysqldump_sql", []byte(realPgDumpSQLTail)) {
		t.Error("expected a pg_dump trailer not to satisfy mysqldump_sql")
	}
}

func TestNamesSortedAndComplete(t *testing.T) {
	names := Names()
	want := []string{"existing_connection", "mysqldump_sql", "pg_dump_custom", "pg_dump_sql", "pgbackrest"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d] = %q, want %q (Names() should be sorted)", i, names[i], w)
		}
	}
}
