package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"regexp"
	"strings"

	"github.com/ahmadpiran/restoredrill/internal/buildinfo"
	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/drill"
	"github.com/ahmadpiran/restoredrill/internal/notify"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

// usageFlagPrefix turns flag's single-dash usage lines into double-dash,
// matching the docs. Both forms already work as input; this is cosmetic.
var usageFlagPrefix = regexp.MustCompile(`(?m)^  -`)

// singleDashFlagRef matches flag's " -name" references inside its own
// one-line parse errors (e.g. "flag provided but not defined: -triger").
var singleDashFlagRef = regexp.MustCompile(`(\s)-([A-Za-z][\w-]*)`)

// dashRewriter rewrites flag's error text from single- to double-dash as
// it's written, so parse-error messages match what users are told to type.
type dashRewriter struct{ w io.Writer }

func (d dashRewriter) Write(p []byte) (int, error) {
	if _, err := d.w.Write(singleDashFlagRef.ReplaceAll(p, []byte("$1--$2"))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(dashRewriter{os.Stderr})

	cfgPath := fs.String("config", "restoredrill.yml", "path to config file")
	trigger := fs.String("trigger", "scheduled", `how this drill was triggered: "scheduled" or "manual"`)
	triggeredBy := fs.String("triggered-by", "", "who triggered this drill (defaults to the OS user for manual triggers)")
	pipelineJobID := fs.String("pipeline-job-id", "", "CI/pipeline job identifier to record on the report (auto-detected from GITHUB_RUN_ID/CI_JOB_ID/BUILD_ID if unset)")
	showVersion := fs.Bool("version", false, "print version information and exit")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		var buf strings.Builder
		realOutput := fs.Output()
		fs.SetOutput(&buf)
		fs.PrintDefaults()
		fs.SetOutput(realOutput)
		fmt.Fprint(os.Stderr, usageFlagPrefix.ReplaceAllString(buf.String(), "  --"))
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2) // error and usage already printed by fs.Parse
	}

	if *showVersion {
		fmt.Printf("restoredrill %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return
	}

	if *trigger != "scheduled" && *trigger != "manual" {
		fmt.Fprintln(os.Stderr, `restoredrill: --trigger must be "scheduled" or "manual"`)
		os.Exit(2)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "restoredrill:", err)
		os.Exit(2)
	}

	rep, runErr := drill.Run(cfg)
	rep.TriggeredBy = *trigger
	rep.TriggeredByUser = resolveTriggeredByUser(*trigger, *triggeredBy)
	rep.PipelineJobID = resolvePipelineJobID(*pipelineJobID)
	rep.ReportGeneratedAt = report.Now()

	// Finalize before any consumer (file or notify sink) sees the report.
	rep.Finalize()

	notifyOK := notify.Send(cfg, rep)
	if !notifyOK {
		for _, e := range rep.NotifyErrors {
			fmt.Fprintln(os.Stderr, "restoredrill: notify:", e)
		}
	}

	writeErr := rep.Write(cfg.Report.Path)
	if writeErr != nil {
		fmt.Fprintln(os.Stderr, "restoredrill: writing report:", writeErr)
	}

	if rep.KeptContainer != "" {
		fmt.Printf("restoredrill: sandbox kept for inspection: docker exec -it %s psql -U postgres\n", rep.KeptContainer)
		fmt.Printf("restoredrill: remove it with: docker rm -f %s\n", rep.KeptContainer)
	}

	failed := len(rep.FailedChecks())
	switch {
	case runErr != nil:
		fmt.Fprintln(os.Stderr, "restoredrill: FAIL:", runErr)
		os.Exit(1)
	case !rep.Passed:
		fmt.Fprintf(os.Stderr, "restoredrill: FAIL: %d check(s) failed, see %s\n", failed, cfg.Report.Path)
		os.Exit(1)
	case writeErr != nil:
		// Drill passed but the evidence never made it to disk: still a failure.
		fmt.Fprintln(os.Stderr, "restoredrill: FAIL: drill passed but report could not be written")
		os.Exit(1)
	case !notifyOK:
		// A broken notify sink means nobody finds out: same failure mode.
		fmt.Fprintf(os.Stderr, "restoredrill: FAIL: drill passed but %d notification(s) failed to deliver, see %s\n", len(rep.NotifyErrors), cfg.Report.Path)
		os.Exit(1)
	default:
		fmt.Printf("restoredrill: PASS, restore took %.1fs, %d/%d checks passed, report: %s\n",
			rep.RestoreDurationSeconds, len(rep.Checks), len(rep.Checks), cfg.Report.Path)
	}
}

// resolveTriggeredByUser fills in "who" for manual triggers if not passed explicitly.
func resolveTriggeredByUser(trigger, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if trigger != "manual" {
		return ""
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return ""
}

// resolvePipelineJobID auto-detects a job ID from common CI env vars.
func resolvePipelineJobID(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, kv := range []struct{ env, prefix string }{
		{"GITHUB_RUN_ID", "github:"},
		{"CI_JOB_ID", "gitlab:"},
		{"BUILD_ID", "jenkins:"},
	} {
		if v := os.Getenv(kv.env); v != "" {
			return kv.prefix + v
		}
	}
	return ""
}
