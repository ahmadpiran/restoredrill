// Package report defines restoredrill's JSON evidence report. Every field is
// always present (never omitted), since auditor workflows often copy
// columns into a spreadsheet.
package report

import (
	"encoding/json"
	"os"
	"time"
)

// tsLayout is the auditor-facing timestamp format: sortable and
// copy-paste-friendly, not Go's default RFC3339.
const tsLayout = "2006-01-02 15:04:05 UTC"

// Timestamp marshals to and parses tsLayout instead of RFC3339. A zero
// Timestamp marshals to "" so the key is always present.
type Timestamp struct {
	time.Time
}

// Now returns the current time as a Timestamp, truncated to UTC.
func Now() Timestamp { return Timestamp{time.Now().UTC()} }

func At(t time.Time) Timestamp { return Timestamp{t.UTC()} }

func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return json.Marshal("")
	}
	return json.Marshal(t.UTC().Format(tsLayout))
}

func (t *Timestamp) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := time.Parse(tsLayout, s)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}

type CheckResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// ValidationError is a structured record of one failed check, surfaced as
// its own top-level field: an auditor reviewing a failed run wants to see
// *what* failed, not just that something did.
type ValidationError struct {
	Check   string `json:"check"`
	Details string `json:"details"`
}

// BackupCandidate is one object considered under an S3 prefix source.
type BackupCandidate struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type Report struct {
	// Version is the build version that produced this report (internal/buildinfo).
	Version string `json:"version"`

	// backup.format. Lets a reader tell an existing_connection drill (no
	// restore performed) apart from one where Restore*/RTO* are zero for
	// another reason.
	RestoreMethod string `json:"restore_method"`

	// Provenance: how and by whom this drill was triggered, same schema
	// whether it ran on a schedule or a human pushed the button.
	TriggeredBy     string `json:"triggered_by"` // "scheduled" or "manual"
	TriggeredByUser string `json:"triggered_by_user"`
	PipelineJobID   string `json:"pipeline_job_id"`

	StartedAt  Timestamp `json:"started_at"`
	FinishedAt Timestamp `json:"finished_at"`

	BackupSource  string `json:"backup_source"`
	GlobalsSource string `json:"globals_source"`
	// BackupResolvedKey is the actual file/object drilled (differs from
	// BackupSource for a prefix source).
	BackupResolvedKey string    `json:"backup_resolved_key"`
	BackupSizeBytes   int64     `json:"backup_size_bytes"`
	BackupTimestamp   Timestamp `json:"backup_timestamp"`
	BackupAgeSeconds  float64   `json:"backup_age_seconds"`
	// BackupCandidatesConsidered: every S3 prefix candidate tried, in
	// order, with why each was skipped (empty for the selected one).
	// Empty for non-prefix sources.
	BackupCandidatesConsidered []BackupCandidate `json:"backup_candidates_considered"`

	PostgresImage string `json:"postgres_image"`
	MySQLImage    string `json:"mysql_image"`

	RestoreStartedAt       Timestamp `json:"restore_initiated_at"`
	RestoreFinishedAt      Timestamp `json:"restore_completed_at"`
	RestoreDurationSeconds float64   `json:"restore_duration_seconds"`

	// *Met is null unless the corresponding target is configured, so "not
	// evaluated" is never confused with "target missed".
	RPOTargetSeconds float64 `json:"rpo_target_seconds"`
	RPOMet           *bool   `json:"rpo_met"`
	RTOTargetSeconds float64 `json:"rto_target_seconds"`
	RTOMet           *bool   `json:"rto_met"`

	Checks           []CheckResult     `json:"checks"`
	ValidationErrors []ValidationError `json:"validation_errors"`

	Passed bool   `json:"passed"`
	Error  string `json:"error"`

	// NotifyErrors records delivery failures; a broken webhook is a finding,
	// not a silent no-op.
	NotifyErrors []string `json:"notify_errors"`

	// KeptContainer names a container left running for inspection
	// (sandbox.keep). ContainerCleanupError records a failed teardown.
	KeptContainer         string `json:"kept_container"`
	ContainerCleanupError string `json:"container_cleanup_error"`

	ReportGeneratedAt Timestamp `json:"report_generated_at"`
}

func (r *Report) FailedChecks() []CheckResult {
	var failed []CheckResult
	for _, c := range r.Checks {
		if !c.Passed {
			failed = append(failed, c)
		}
	}
	return failed
}

// Finalize recomputes ValidationErrors from Checks and normalizes empty
// slices to `[]`. Must run before any consumer (file or notify sink) sees
// the report, so both see the same shape.
func (r *Report) Finalize() {
	r.ValidationErrors = make([]ValidationError, 0, len(r.Checks))
	for _, c := range r.Checks {
		if !c.Passed {
			r.ValidationErrors = append(r.ValidationErrors, ValidationError{Check: c.Name, Details: c.Details})
		}
	}
	if r.Checks == nil {
		r.Checks = []CheckResult{}
	}
	if r.NotifyErrors == nil {
		r.NotifyErrors = []string{}
	}
	if r.BackupCandidatesConsidered == nil {
		r.BackupCandidatesConsidered = []BackupCandidate{}
	}
}

// Write serializes the report to path. Callers must call Finalize first;
// Write itself does not normalize.
func (r *Report) Write(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
