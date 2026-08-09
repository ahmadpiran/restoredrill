package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimestampMarshalFormat(t *testing.T) {
	ts := At(time.Date(2026, 7, 26, 14, 30, 5, 0, time.UTC))
	b, err := json.Marshal(ts)
	if err != nil {
		t.Fatal(err)
	}
	// Sortable, copy-paste-friendly format, not Go's default RFC3339.
	want := `"2026-07-26 14:30:05 UTC"`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

func TestTimestampZeroMarshalsEmptyString(t *testing.T) {
	b, err := json.Marshal(Timestamp{})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `""` {
		t.Errorf("got %s, want an empty string so the key is always present", b)
	}
}

func TestTimestampRoundTrip(t *testing.T) {
	orig := At(time.Date(2026, 7, 26, 14, 30, 5, 0, time.UTC))
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got Timestamp
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(orig.Time) {
		t.Errorf("round-trip mismatch: got %v, want %v", got.Time, orig.Time)
	}
}

func TestFinalizePopulatesValidationErrorsFromFailedChecks(t *testing.T) {
	r := &Report{
		Checks: []CheckResult{
			{Name: "a", Passed: true},
			{Name: "b", Passed: false, Details: "boom"},
		},
	}
	r.Finalize()
	if len(r.ValidationErrors) != 1 || r.ValidationErrors[0].Check != "b" || r.ValidationErrors[0].Details != "boom" {
		t.Errorf("expected exactly one validation error for check b, got %+v", r.ValidationErrors)
	}
}

func TestFinalizeNormalizesEmptySlicesNotNull(t *testing.T) {
	r := &Report{}
	r.Finalize()
	if r.ValidationErrors == nil || r.Checks == nil || r.NotifyErrors == nil || r.BackupCandidatesConsidered == nil {
		t.Errorf("expected empty slices, not nil, for schema stability: checks=%v validation_errors=%v notify_errors=%v candidates=%v",
			r.Checks, r.ValidationErrors, r.NotifyErrors, r.BackupCandidatesConsidered)
	}
}

func TestWriteSerializesCurrentState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	r := &Report{Passed: true}
	r.Finalize()
	if err := r.Write(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Passed || got.Checks == nil || got.ValidationErrors == nil || got.NotifyErrors == nil {
		t.Errorf("got %+v, want a normalized report matching what Finalize produced", got)
	}
}

// notify.Send used to run before Write(), so delivered payloads had null
// fields even though the file on disk was correct. Finalize fixes that.
func TestFinalizeMustPrecedeConsumers(t *testing.T) {
	r := &Report{}
	unfinalized, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(unfinalized, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"checks", "validation_errors", "notify_errors", "backup_candidates_considered"} {
		if string(m[key]) != "null" {
			t.Fatalf("test setup assumption broken: expected %q to be null before Finalize, got %s", key, m[key])
		}
	}

	r.Finalize()
	finalized, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(finalized, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"checks", "validation_errors", "notify_errors", "backup_candidates_considered"} {
		if string(m[key]) != "[]" {
			t.Errorf("after Finalize, expected %q to be [], got %s (a consumer that saw the report before Finalize would get null instead)", key, m[key])
		}
	}
}

// Guards the package's core promise: every field is present in the JSON
// even when blank. omitempty would omit the key instead of marshaling "".
func TestAlwaysPresentFieldsMarshalEvenWhenEmpty(t *testing.T) {
	r := &Report{}
	r.Finalize()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"error", "kept_container", "container_cleanup_error"} {
		if raw, ok := m[key]; !ok {
			t.Errorf("expected key %q always present in report JSON, even when empty", key)
		} else if string(raw) != `""` {
			t.Errorf("expected key %q to be an empty string when unset, got %s", key, raw)
		}
	}
}

func TestCheckResultDetailsAlwaysPresent(t *testing.T) {
	c := CheckResult{Name: "x", Passed: true}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if raw, ok := m["details"]; !ok {
		t.Error(`expected key "details" always present in CheckResult JSON, even when empty`)
	} else if string(raw) != `""` {
		t.Errorf(`expected "details" to be an empty string when unset, got %s`, raw)
	}
}
