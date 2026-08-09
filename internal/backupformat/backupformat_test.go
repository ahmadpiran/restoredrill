package backupformat

import "testing"

func TestValid(t *testing.T) {
	if !Valid("pg_dump_custom") {
		t.Error("expected pg_dump_custom to be valid")
	}
	if !Valid("pg_dump_sql") {
		t.Error("expected pg_dump_sql to be valid")
	}
	if Valid("mysql_dump") {
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
	if Sniffable("mysql_dump") {
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

func TestNamesSortedAndComplete(t *testing.T) {
	names := Names()
	want := []string{"pg_dump_custom", "pg_dump_sql"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d] = %q, want %q (Names() should be sorted)", i, names[i], w)
		}
	}
}
