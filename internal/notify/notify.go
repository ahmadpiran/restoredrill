// Package notify emits drill results into systems teams already watch.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

// Send delivers the report to every configured sink, recording each failure
// on rep.NotifyErrors. Returns true only if every sink succeeded.
func Send(cfg *config.Config, rep *report.Report) bool {
	ok := true

	if cfg.Output.PrometheusTextfile != "" {
		if err := writePrometheus(cfg.Output.PrometheusTextfile, rep); err != nil {
			rep.NotifyErrors = append(rep.NotifyErrors, fmt.Sprintf("prometheus textfile: %v", err))
			ok = false
		}
	}
	if cfg.Notify.WebhookURL != "" {
		body, _ := json.Marshal(rep)
		if err := post(cfg.Notify.WebhookURL, "application/json", body); err != nil {
			rep.NotifyErrors = append(rep.NotifyErrors, fmt.Sprintf("webhook: %v", err))
			ok = false
		}
	}
	if cfg.Notify.SlackWebhookURL != "" {
		payload, _ := json.Marshal(map[string]string{"text": slackText(rep)})
		if err := post(cfg.Notify.SlackWebhookURL, "application/json", payload); err != nil {
			rep.NotifyErrors = append(rep.NotifyErrors, fmt.Sprintf("slack: %v", err))
			ok = false
		}
	}

	return ok
}

// writePrometheus emits node_exporter textfile-collector metrics; alert on
// the timestamp metric's age to catch a stopped drill.
func writePrometheus(path string, rep *report.Report) error {
	passed := 0
	if rep.Passed {
		passed = 1
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP restoredrill_passed 1 if the last restore drill passed.\n")
	fmt.Fprintf(&b, "# TYPE restoredrill_passed gauge\nrestoredrill_passed %d\n", passed)
	fmt.Fprintf(&b, "# HELP restoredrill_last_run_timestamp_seconds Unix time of the last drill.\n")
	fmt.Fprintf(&b, "# TYPE restoredrill_last_run_timestamp_seconds gauge\nrestoredrill_last_run_timestamp_seconds %d\n", rep.FinishedAt.Unix())
	fmt.Fprintf(&b, "# HELP restoredrill_restore_duration_seconds Measured restore duration.\n")
	fmt.Fprintf(&b, "# TYPE restoredrill_restore_duration_seconds gauge\nrestoredrill_restore_duration_seconds %g\n", rep.RestoreDurationSeconds)
	fmt.Fprintf(&b, "# HELP restoredrill_checks_failed Number of failed checks in the last drill.\n")
	fmt.Fprintf(&b, "# TYPE restoredrill_checks_failed gauge\nrestoredrill_checks_failed %d\n", len(rep.FailedChecks()))
	fmt.Fprintf(&b, "# HELP restoredrill_backup_size_bytes Size of the backup that was drilled.\n")
	fmt.Fprintf(&b, "# TYPE restoredrill_backup_size_bytes gauge\nrestoredrill_backup_size_bytes %d\n", rep.BackupSizeBytes)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func slackText(rep *report.Report) string {
	status := "✅ PASS"
	if !rep.Passed {
		status = "❌ FAIL"
	}
	msg := fmt.Sprintf("restoredrill %s, restore %.1fs, %d/%d checks passed (%s)",
		status, rep.RestoreDurationSeconds, len(rep.Checks)-len(rep.FailedChecks()), len(rep.Checks), rep.BackupSource)
	if rep.Error != "" {
		msg += "\nerror: " + rep.Error
	}
	for _, c := range rep.FailedChecks() {
		msg += fmt.Sprintf("\n• failed: %s (%s)", c.Name, c.Details)
	}
	return msg
}

func post(url, contentType string, body []byte) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, contentType, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
