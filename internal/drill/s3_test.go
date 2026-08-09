package drill

import (
	"testing"
	"time"
)

func TestParseS3LsOutput(t *testing.T) {
	in := "                           PRE subfolder/\n" +
		"2024-01-02 03:04:05        123 backup-2024-01-02.dump\n" +
		"2024-01-03 03:04:05        456 backup-2024-01-03.dump\n" +
		"not a valid line\n" +
		"\n"
	entries := parseS3LsOutput(in)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (PRE line and garbage line skipped), got %d: %+v", len(entries), entries)
	}
	if entries[0].name != "backup-2024-01-02.dump" || entries[0].size != 123 {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].name != "backup-2024-01-03.dump" || entries[1].size != 456 {
		t.Errorf("unexpected second entry: %+v", entries[1])
	}
}

func TestSortS3EntriesNewestFirstByTimestampNotName(t *testing.T) {
	older, _ := time.Parse("2006-01-02 15:04:05", "2024-01-02 03:04:05")
	newer, _ := time.Parse("2006-01-02 15:04:05", "2024-01-10 00:00:00")
	entries := []s3Entry{
		{name: "z-old.dump", timestamp: older},
		{name: "a-new.dump", timestamp: newer},
	}
	sortS3EntriesNewestFirst(entries)
	if entries[0].name != "a-new.dump" {
		t.Errorf("expected the newer entry (a-new.dump) first regardless of name order, got %q", entries[0].name)
	}
}

func TestSortS3EntriesNewestFirstEmptyInput(t *testing.T) {
	entries := []s3Entry(nil)
	sortS3EntriesNewestFirst(entries) // must not panic
	if len(entries) != 0 {
		t.Errorf("expected still-empty slice, got %+v", entries)
	}
}

// A sibling key sharing a string prefix (e.g. a checksum file) can appear
// in the same aws s3 ls listing; matchS3EntryByName must pick by exact name.
func TestMatchS3EntryByNameIgnoresPrefixSiblings(t *testing.T) {
	older, _ := time.Parse("2006-01-02 15:04:05", "2024-01-01 00:00:00")
	newer, _ := time.Parse("2006-01-02 15:04:05", "2024-01-10 00:00:00")
	entries := []s3Entry{
		// Adversarial order: proves selection is by name, not position.
		{name: "prod.dump.sha256", timestamp: older},
		{name: "prod.dump", timestamp: newer},
	}
	got, ok := matchS3EntryByName(entries, "prod.dump")
	if !ok {
		t.Fatal("expected a match for prod.dump")
	}
	if got.name != "prod.dump" || !got.timestamp.Equal(newer) {
		t.Errorf("expected the prod.dump entry (timestamp %v), got %+v", newer, got)
	}
}

// aws s3 ls renders LastModified in local time, not UTC; time.Parse
// defaulting to UTC caused a real negative-age bug. Pins the fix without
// depending on the test machine's timezone.
func TestParseS3LsOutputInterpretsLocalTimezone(t *testing.T) {
	orig := time.Local
	defer func() { time.Local = orig }()
	time.Local = time.FixedZone("TEST+0300", 3*60*60)

	in := "2024-06-15 12:00:00        100 backup.dump\n"
	entries := parseS3LsOutput(in)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(entries), entries)
	}
	got := entries[0].timestamp.UTC()
	want := time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC) // 12:00 at UTC+3 == 09:00 UTC
	if !got.Equal(want) {
		t.Errorf("parsed timestamp in UTC = %v, want %v (local 12:00:00 at UTC+3 must convert to 09:00:00 UTC, not be treated as already UTC)", got, want)
	}
}

func TestMatchS3EntryByNameNoMatch(t *testing.T) {
	entries := []s3Entry{{name: "prod.dump.sha256", timestamp: time.Now()}}
	if _, ok := matchS3EntryByName(entries, "prod.dump"); ok {
		t.Error("expected no match when the exact name isn't present")
	}
}
