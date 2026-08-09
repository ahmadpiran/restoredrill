package drill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ts(s string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", s)
	return t
}

// The exact repro: a checksum sidecar uploaded after the real dump must not
// win just for being newest. try simulates the PGDMP header check.
func TestSelectS3CandidateSkipsNonMatchingSidecar(t *testing.T) {
	entries := []s3Entry{
		{name: "backup-new.dump.sha256", timestamp: ts("2024-01-10 00:02:00")},
		{name: "backup-new.dump", timestamp: ts("2024-01-10 00:00:00")},
		{name: "backup-old.dump", timestamp: ts("2024-01-09 00:00:00")},
	}
	sortS3EntriesNewestFirst(entries)

	var tried []string
	try := func(key string) (string, func(), string, error) {
		tried = append(tried, key)
		if key == "s3://bucket/prefix/backup-new.dump" {
			return "/tmp/local-dump", func() {}, "", nil
		}
		return "", nil, "does not look like a pg_dump_custom file", nil
	}

	res, err := selectS3Candidate("s3://bucket/prefix/", entries, "", try)
	if err != nil {
		t.Fatalf("expected a selection, got error: %v", err)
	}
	if res.resolvedKey != "s3://bucket/prefix/backup-new.dump" {
		t.Errorf("expected backup-new.dump selected, got %q", res.resolvedKey)
	}
	if len(tried) != 2 {
		t.Fatalf("expected the sidecar tried first, then the real dump; got %v", tried)
	}
	if len(res.candidates) != 2 || res.candidates[1].Reason != "" {
		t.Errorf("expected 2 candidates recorded with the last one selected (empty reason), got %+v", res.candidates)
	}
}

func TestSelectS3CandidateHonorsPattern(t *testing.T) {
	entries := []s3Entry{
		{name: "backup.dump.sha256", timestamp: ts("2024-01-10 00:02:00")},
		{name: "backup.dump", timestamp: ts("2024-01-10 00:00:00")},
	}
	sortS3EntriesNewestFirst(entries)

	var tried []string
	try := func(key string) (string, func(), string, error) {
		tried = append(tried, key)
		return "/tmp/local", func() {}, "", nil
	}

	res, err := selectS3Candidate("s3://bucket/prefix/", entries, "*.dump", try)
	if err != nil {
		t.Fatalf("expected a selection, got error: %v", err)
	}
	if res.resolvedKey != "s3://bucket/prefix/backup.dump" {
		t.Errorf("expected the pattern to skip the sidecar entirely, got %q", res.resolvedKey)
	}
	if len(tried) != 1 {
		t.Errorf("expected only the matching candidate to be tried, got %v", tried)
	}
}

func TestSelectS3CandidateStopsAtBound(t *testing.T) {
	var entries []s3Entry
	for i := 0; i < maxS3PrefixCandidates+3; i++ {
		entries = append(entries, s3Entry{name: "junk", timestamp: ts("2024-01-10 00:00:00").Add(-time.Duration(i) * time.Hour)})
	}

	calls := 0
	try := func(key string) (string, func(), string, error) {
		calls++
		return "", nil, "does not look like a backup", nil
	}

	_, err := selectS3Candidate("s3://bucket/prefix/", entries, "", try)
	if err == nil {
		t.Fatal("expected an error: no candidate ever matches")
	}
	if calls != maxS3PrefixCandidates {
		t.Errorf("expected exactly %d attempts (the bound), got %d", maxS3PrefixCandidates, calls)
	}
}

func TestSelectS3CandidateRecordsDownloadFailure(t *testing.T) {
	entries := []s3Entry{{name: "backup.dump", timestamp: ts("2024-01-10 00:00:00")}}
	try := func(key string) (string, func(), string, error) {
		return "", nil, "", errors.New("aws s3 cp: access denied")
	}

	_, err := selectS3Candidate("s3://bucket/prefix/", entries, "", try)
	if err == nil {
		t.Fatal("expected an error: the only candidate failed to download")
	}
}

func TestReadHead(t *testing.T) {
	short := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(short, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, err := readHead(short, sniffHeadBytes)
	if err != nil {
		t.Fatalf("expected no error reading a file shorter than n, got %v", err)
	}
	if string(head) != "hi" {
		t.Errorf("expected the short file's full content, got %q", head)
	}

	long := filepath.Join(t.TempDir(), "long")
	if err := os.WriteFile(long, []byte("PGDMP\x01\x0e\x00extra trailing content"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, err = readHead(long, sniffHeadBytes)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(head) != sniffHeadBytes {
		t.Errorf("expected exactly %d bytes, got %d", sniffHeadBytes, len(head))
	}
}

func TestSelectS3CandidateAcceptsFirstWhenTryNeverRejects(t *testing.T) {
	// Mirrors an unsniffable format: try downloads but never verifies
	// content, so the newest pattern-matched candidate always wins.
	entries := []s3Entry{
		{name: "backup.sql", timestamp: ts("2024-01-10 00:00:00")},
		{name: "backup-old.sql", timestamp: ts("2024-01-09 00:00:00")},
	}
	try := func(key string) (string, func(), string, error) {
		return "/tmp/local", func() {}, "", nil
	}

	res, err := selectS3Candidate("s3://bucket/prefix/", entries, "", try)
	if err != nil {
		t.Fatalf("expected a selection, got error: %v", err)
	}
	if res.resolvedKey != "s3://bucket/prefix/backup.sql" {
		t.Errorf("expected the newest candidate accepted immediately, got %q", res.resolvedKey)
	}
}
