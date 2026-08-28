package drill

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/backupformat"
	"github.com/ahmadpiran/restoredrill/internal/buildinfo"
	"github.com/ahmadpiran/restoredrill/internal/config"
	"github.com/ahmadpiran/restoredrill/internal/report"
)

// Run executes one restore drill. Always returns a report; err is the fatal
// error, if any. Checks are fail-closed: one that can't run is a failure,
// never a skip.
func Run(cfg *config.Config) (*report.Report, error) {
	rep := &report.Report{
		Version:       buildinfo.Version,
		StartedAt:     report.Now(),
		BackupSource:  cfg.Backup.Source,
		GlobalsSource: cfg.Backup.GlobalsSource,
		PostgresImage: cfg.Postgres.Image,
	}
	defer func() { rep.FinishedAt = report.Now() }()

	// Set early so a drill that fails before restore and an unconfigured
	// target don't both read as 0/null.
	if cfg.Checks.RTOTargetDuration > 0 {
		rep.RTOTargetSeconds = cfg.Checks.RTOTargetDuration.Seconds()
	}

	fr, err := fetch(cfg.Backup)
	if err != nil {
		return fail(rep, cfg.Checks, fmt.Errorf("fetching backup: %w", err))
	}
	if fr.cleanup != nil {
		defer fr.cleanup()
	}
	rep.BackupResolvedKey = fr.resolvedKey
	for _, c := range fr.candidates {
		rep.BackupCandidatesConsidered = append(rep.BackupCandidatesConsidered, report.BackupCandidate{Name: c.Name, Reason: c.Reason})
	}

	var gfr fetchResult
	if cfg.Backup.GlobalsSource != "" {
		gfr, err = fetchExact(cfg.Backup.GlobalsSource)
		if err != nil {
			return fail(rep, cfg.Checks, fmt.Errorf("fetching globals: %w", err))
		}
		if gfr.cleanup != nil {
			defer gfr.cleanup()
		}
		if _, err := os.Stat(gfr.localPath); err != nil {
			return fail(rep, cfg.Checks, fmt.Errorf("globals file not found: %w", err))
		}
	}

	fi, err := os.Stat(fr.localPath)
	if err != nil {
		return fail(rep, cfg.Checks, fmt.Errorf("backup not found: %w", err))
	}
	rep.BackupSizeBytes = fi.Size()

	// Prefer the source's own timestamp over local mtime, which is only
	// "when we downloaded it".
	backupTime := fr.timestamp
	if backupTime.IsZero() {
		backupTime = fi.ModTime()
	}
	if !backupTime.IsZero() {
		rep.BackupTimestamp = report.At(backupTime)
		rep.BackupAgeSeconds = time.Since(backupTime).Seconds()
	}

	if cfg.Checks.MinSizeBytes > 0 {
		rep.Checks = append(rep.Checks, report.CheckResult{
			Name:    fmt.Sprintf("precheck: backup at least %d bytes", cfg.Checks.MinSizeBytes),
			Passed:  fi.Size() >= cfg.Checks.MinSizeBytes,
			Details: fmt.Sprintf("backup is %d bytes", fi.Size()),
		})
		if fi.Size() < cfg.Checks.MinSizeBytes {
			return fail(rep, cfg.Checks, fmt.Errorf("backup smaller than min_size_bytes (%d < %d)", fi.Size(), cfg.Checks.MinSizeBytes))
		}
	}

	// RPO precheck: is the backup fresh, or did the backup cron silently stall?
	if cfg.Checks.RPOTargetDuration > 0 {
		rep.RPOTargetSeconds = cfg.Checks.RPOTargetDuration.Seconds()
		met := !backupTime.IsZero() && time.Since(backupTime) <= cfg.Checks.RPOTargetDuration
		rep.RPOMet = &met
		res := report.CheckResult{Name: fmt.Sprintf("precheck: RPO target met (backup fresher than %s)", cfg.Checks.RPOTargetDuration)}
		if backupTime.IsZero() {
			res.Details = "backup timestamp could not be determined"
		} else {
			res.Details = fmt.Sprintf("backup is %s old", time.Since(backupTime).Round(time.Second))
		}
		res.Passed = met
		rep.Checks = append(rep.Checks, res)
		if !met {
			return fail(rep, cfg.Checks, fmt.Errorf("RPO target check failed: %s", res.Details))
		}
	}

	if cfg.Backup.Format == "pg_dump_sql" &&
		(cfg.Checks.ArchiveIntegrity == nil || *cfg.Checks.ArchiveIntegrity) {
		tail, terr := readTail(fr.localPath, tailReadBytes)
		complete := terr == nil && backupformat.Complete(cfg.Backup.Format, tail)
		res := report.CheckResult{Name: "precheck: dump file complete (trailer present)", Passed: complete}
		if !complete {
			if terr != nil {
				res.Details = terr.Error()
			} else {
				res.Details = "no completion trailer found in file tail"
			}
		}
		rep.Checks = append(rep.Checks, res)
		if !complete {
			return fail(rep, cfg.Checks, fmt.Errorf("dump completeness check failed: %s", res.Details))
		}
	}

	sb := newSandbox(cfg.Postgres.Image)
	keep := false
	// Registered before sb.start() so a readiness timeout still cleans up.
	defer func() {
		if !sb.created {
			return
		}
		switch cfg.Sandbox.Keep {
		case "always":
			keep = true
		case "on-failure":
			keep = !rep.Passed
		}
		if keep {
			rep.KeptContainer = sb.name
			return
		}
		if err := sb.destroy(); err != nil {
			rep.ContainerCleanupError = err.Error()
			fmt.Fprintf(os.Stderr, "restoredrill: warning: failed to remove container %s: %v\n", sb.name, err)
		}
	}()
	if err := sb.start(); err != nil {
		return fail(rep, cfg.Checks, err)
	}

	if cfg.Backup.GlobalsSource != "" {
		const remoteGlobals = "/tmp/restoredrill-globals.sql"
		res := report.CheckResult{Name: "precheck: globals restored (roles/grants available)"}
		if err := sb.copyIn(gfr.localPath, remoteGlobals); err != nil {
			res.Details = err.Error()
		} else if out, err := sb.exec("psql", "-U", "postgres", "-d", "postgres", "-f", remoteGlobals); err != nil {
			res.Details = firstLine(out)
		} else if bad := unexpectedGlobalsErrors(out); len(bad) > 0 {
			res.Details = strings.Join(bad, "; ")
		} else {
			res.Passed = true
		}
		rep.Checks = append(rep.Checks, res)
		if !res.Passed {
			return fail(rep, cfg.Checks, fmt.Errorf("globals restore failed: %s", res.Details))
		}

		if cfg.Checks.VerifyAsRole != "" {
			roleRes := report.CheckResult{Name: "precheck: verify_as_role exists"}
			exists, err := roleExists(sb, cfg.Checks.VerifyAsRole)
			switch {
			case err != nil:
				roleRes.Details = firstLine(err.Error())
			case !exists:
				roleRes.Details = fmt.Sprintf("role %q not found in the sandbox after globals restore; check backup.globals_source contains it", cfg.Checks.VerifyAsRole)
			default:
				roleRes.Passed = true
			}
			rep.Checks = append(rep.Checks, roleRes)
			if !roleRes.Passed {
				return fail(rep, cfg.Checks, fmt.Errorf("verify_as_role check failed: %s", roleRes.Details))
			}
		}
	}

	const remote = "/tmp/restoredrill-backup"
	if err := sb.copyIn(fr.localPath, remote); err != nil {
		return fail(rep, cfg.Checks, err)
	}

	if cfg.Backup.Format == "pg_dump_custom" &&
		(cfg.Checks.ArchiveIntegrity == nil || *cfg.Checks.ArchiveIntegrity) {
		out, err := sb.exec("pg_restore", "--list", remote)
		res := report.CheckResult{Name: "precheck: archive integrity (TOC readable)", Passed: err == nil}
		if err != nil {
			res.Details = firstLine(out)
		}
		rep.Checks = append(rep.Checks, res)
		if err != nil {
			return fail(rep, cfg.Checks, fmt.Errorf("archive integrity check failed: %v: %s", err, firstLine(out)))
		}
	}

	rep.RestoreStartedAt = report.Now()
	restoreStart := time.Now()
	if err := restore(sb, cfg.Backup.Format, remote, cfg.Backup.GlobalsSource != ""); err != nil {
		return fail(rep, cfg.Checks, fmt.Errorf("restore failed: %w", err))
	}
	rep.RestoreDurationSeconds = time.Since(restoreStart).Seconds()
	rep.RestoreFinishedAt = report.Now()

	if cfg.Checks.RTOTargetDuration > 0 {
		met := rep.RestoreDurationSeconds <= cfg.Checks.RTOTargetDuration.Seconds()
		rep.RTOMet = &met
		rep.Checks = append(rep.Checks, report.CheckResult{
			Name:    fmt.Sprintf("RTO target met (restore under %s)", cfg.Checks.RTOTargetDuration),
			Passed:  met,
			Details: fmt.Sprintf("restore took %.1fs", rep.RestoreDurationSeconds),
		})
	}

	rep.Checks = append(rep.Checks, runChecks(sb, cfg.Checks)...)
	rep.Passed = true
	for _, c := range rep.Checks {
		if !c.Passed {
			rep.Passed = false
		}
	}
	return rep, nil
}

func fail(rep *report.Report, cfg config.Checks, err error) (*report.Report, error) {
	rep.Error = err.Error()
	rep.Passed = false

	seen := make(map[string]bool, len(rep.Checks))
	for _, c := range rep.Checks {
		seen[c.Name] = true
	}
	for _, name := range expectedCheckNames(cfg) {
		if !seen[name] {
			rep.Checks = append(rep.Checks, report.CheckResult{
				Name:    name,
				Passed:  false,
				Details: "not evaluated: drill aborted before this check could run",
			})
		}
	}

	return rep, err
}

// expectedCheckNames returns each configured check's Name, matching the
// format runChecks/the RTO check produce.
func expectedCheckNames(cfg config.Checks) []string {
	var names []string
	if cfg.RTOTargetDuration > 0 {
		names = append(names, fmt.Sprintf("RTO target met (restore under %s)", cfg.RTOTargetDuration))
	}
	if cfg.MinTables > 0 {
		names = append(names, fmt.Sprintf("at least %d tables restored", cfg.MinTables))
	}
	if cfg.SequenceIntegrity {
		names = append(names, "sequence integrity")
	}
	for _, rc := range cfg.RowCounts {
		names = append(names, fmt.Sprintf("%s has at least %d rows", rc.Table, rc.Min))
	}
	for _, q := range cfg.Queries {
		names = append(names, q.Name)
	}
	return names
}

// fetchResult describes a resolved backup ready to be restored.
type fetchResult struct {
	localPath string
	// resolvedKey is the actual file/object drilled (may differ from the
	// configured source for an s3:// prefix).
	resolvedKey string
	// timestamp is the backup's own timestamp (S3 LastModified), zero if
	// unknown.
	timestamp time.Time
	cleanup   func()
	// candidates records every S3 prefix candidate tried, in order, with
	// why each was skipped (empty for the selected one). Non-prefix: empty.
	candidates []s3Candidate
}

// s3Candidate is one object considered under an S3 prefix source.
type s3Candidate struct {
	Name   string
	Reason string
}

// fetch resolves the backup source to a local file, downloading if needed.
func fetch(b config.Backup) (fetchResult, error) {
	switch {
	case strings.HasPrefix(b.Source, "s3://"):
		return fetchS3(b)
	case strings.HasPrefix(b.Source, "file://"):
		u, err := url.Parse(b.Source)
		if err != nil {
			return fetchResult{}, err
		}
		local := filepath.FromSlash(stripWindowsFileURLPath(u.Path, runtime.GOOS))
		return fetchResult{localPath: local, resolvedKey: local}, nil
	default:
		return fetchResult{localPath: b.Source, resolvedKey: b.Source}, nil
	}
}

// fetchExact resolves a globals_source: like fetch, but no S3 prefix/newest-object selection.
func fetchExact(source string) (fetchResult, error) {
	switch {
	case strings.HasPrefix(source, "s3://"):
		return fetchS3ExactKey(source)
	case strings.HasPrefix(source, "file://"):
		u, err := url.Parse(source)
		if err != nil {
			return fetchResult{}, err
		}
		local := filepath.FromSlash(stripWindowsFileURLPath(u.Path, runtime.GOOS))
		return fetchResult{localPath: local, resolvedKey: local}, nil
	default:
		return fetchResult{localPath: source, resolvedKey: source}, nil
	}
}

// stripWindowsFileURLPath strips the leading slash from a file:// URL's
// drive-letter path (e.g. "/C:/x") on Windows only; elsewhere that shape is
// a real path and must be left alone.
func stripWindowsFileURLPath(p, goos string) string {
	if goos == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		return p[1:]
	}
	return p
}

// s3Entry is one parsed line of `aws s3 ls` output.
type s3Entry struct {
	name      string
	timestamp time.Time
	size      int64
}

// parseS3LsOutput parses `aws s3 ls` output (DATE TIME SIZE NAME per line,
// "PRE" for sub-prefixes), skipping unparseable lines.
//
// aws s3 ls renders LastModified in local time, not UTC, so this uses
// time.ParseInLocation with time.Local rather than time.Parse, which
// defaults to UTC and would silently misread it.
func parseS3LsOutput(lsOut string) []s3Entry {
	var entries []s3Entry
	for _, line := range strings.Split(lsOut, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] == "PRE" {
			continue
		}
		ts, err := time.ParseInLocation("2006-01-02 15:04:05", f[0]+" "+f[1], time.Local)
		if err != nil {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		entries = append(entries, s3Entry{name: f[len(f)-1], timestamp: ts, size: size})
	}
	return entries
}

// sortS3EntriesNewestFirst sorts entries by timestamp, newest first, in place.
func sortS3EntriesNewestFirst(entries []s3Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].timestamp.After(entries[j].timestamp)
	})
}

// matchS3EntryByName returns the entry with an exact name match. `aws s3 ls`
// on a full key still prefix-matches, so a sibling like "x.dump.sha256"
// could otherwise donate its timestamp to "x.dump".
func matchS3EntryByName(entries []s3Entry, want string) (s3Entry, bool) {
	for _, e := range entries {
		if e.name == want {
			return e, true
		}
	}
	return s3Entry{}, false
}

// fetchS3 downloads an s3:// object via the aws CLI. A trailing "/" on the
// source makes it a prefix.
func fetchS3(b config.Backup) (fetchResult, error) {
	if strings.HasSuffix(b.Source, "/") {
		return fetchS3Prefix(b)
	}
	return fetchS3ExactKey(b.Source)
}

func fetchS3ExactKey(key string) (fetchResult, error) {
	var backupTime time.Time
	// aws s3 ls prefix-matches even on a full key, so filter to the exact
	// basename.
	if out, err := run("aws", "s3", "ls", key); err == nil {
		if e, ok := matchS3EntryByName(parseS3LsOutput(out), path.Base(key)); ok {
			backupTime = e.timestamp
		}
	}
	local, cleanup, err := downloadS3Object(key)
	if err != nil {
		return fetchResult{}, err
	}
	return fetchResult{localPath: local, resolvedKey: key, timestamp: backupTime, cleanup: cleanup}, nil
}

// maxS3PrefixCandidates bounds how many objects are tried before giving up.
const maxS3PrefixCandidates = 5

// sniffHeadBytes is how much of a candidate is read to check its signature.
const sniffHeadBytes = 16

// tailReadBytes covers the trailer comment block plus a trailing
// \unrestrict token line, with margin.
const tailReadBytes = 512

// fetchS3Prefix walks objects under a prefix newest-first, skipping any
// that don't match S3ObjectPattern and, for sniffable formats, any that
// don't look like the configured format. Unsniffable formats accept the
// first pattern-matched candidate (config.Load requires a pattern then).
func fetchS3Prefix(b config.Backup) (fetchResult, error) {
	out, err := run("aws", "s3", "ls", b.Source)
	if err != nil {
		return fetchResult{}, fmt.Errorf("aws s3 ls %s: %v: %s", b.Source, err, firstLine(out))
	}
	entries := parseS3LsOutput(out)
	if len(entries) == 0 {
		return fetchResult{}, fmt.Errorf("%s: no objects found under prefix", b.Source)
	}
	sortS3EntriesNewestFirst(entries)

	return selectS3Candidate(b.Source, entries, b.S3ObjectPattern, func(key string) (string, func(), string, error) {
		local, cleanup, err := downloadS3Object(key)
		if err != nil {
			return "", nil, "", err
		}
		if backupformat.Sniffable(b.Format) {
			head, _ := readHead(local, sniffHeadBytes)
			if !backupformat.Matches(b.Format, head) {
				cleanup()
				return "", nil, fmt.Sprintf("does not look like a %s file", b.Format), nil
			}
		}
		return local, cleanup, "", nil
	})
}

// selectS3Candidate walks entries (newest-first), skipping non-matching
// names, and calls try on each remaining one (up to maxS3PrefixCandidates)
// until try accepts. Kept separate from the actual download/sniff so it's
// testable without aws.
func selectS3Candidate(source string, entries []s3Entry, pattern string, try func(key string) (localPath string, cleanup func(), rejected string, err error)) (fetchResult, error) {
	var candidates []s3Candidate
	tried := 0
	for _, e := range entries {
		if tried >= maxS3PrefixCandidates {
			break
		}
		if pattern != "" {
			if matched, _ := path.Match(pattern, e.name); !matched {
				continue
			}
		}
		tried++

		key := source + e.name
		local, cleanup, rejected, err := try(key)
		switch {
		case err != nil:
			candidates = append(candidates, s3Candidate{Name: e.name, Reason: err.Error()})
			continue
		case rejected != "":
			candidates = append(candidates, s3Candidate{Name: e.name, Reason: rejected})
			continue
		}

		candidates = append(candidates, s3Candidate{Name: e.name})
		return fetchResult{
			localPath:   local,
			resolvedKey: key,
			timestamp:   e.timestamp,
			cleanup:     cleanup,
			candidates:  candidates,
		}, nil
	}
	return fetchResult{}, fmt.Errorf("%s: no object under prefix matched (tried %d candidate(s) of %d total)", source, tried, len(entries))
}

// downloadS3Object copies key to a fresh temp directory and returns its
// local path and a cleanup func that removes the whole directory.
func downloadS3Object(key string) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "restoredrill-")
	if err != nil {
		return "", nil, err
	}
	local := filepath.Join(tmp, filepath.Base(strings.TrimSuffix(key, "/")))
	if out, err := run("aws", "s3", "cp", key, local); err != nil {
		os.RemoveAll(tmp)
		return "", nil, fmt.Errorf("aws s3 cp %s: %v: %s", key, err, firstLine(out))
	}
	return local, func() { os.RemoveAll(tmp) }, nil
}

// readHead reads up to n leading bytes of the file at path.
func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:m], nil
}

// readTail reads up to the last n bytes of the file at path.
func readTail(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() < int64(n) {
		n = int(fi.Size())
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, fi.Size()-int64(n)); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

// unexpectedGlobalsErrors ignores only the expected "postgres" role
// collision (pg_dumpall redeclares it); ON_ERROR_STOP would abort there and
// skip every later role/grant statement alphabetically.
func unexpectedGlobalsErrors(output string) []string {
	var bad []string
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "ERROR:") {
			continue
		}
		if strings.Contains(line, `role "postgres" already exists`) {
			continue
		}
		bad = append(bad, strings.TrimSpace(line))
	}
	return bad
}

func roleExists(sb *sandbox, role string) (bool, error) {
	out, err := sb.query("SELECT 1 FROM pg_roles WHERE rolname = " + quoteLiteral(role))
	return out == "1", err
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func restore(sb *sandbox, format, remote string, preserveOwnership bool) error {
	var out string
	var err error
	switch format {
	case "pg_dump_custom":
		args := []string{"-U", "postgres", "-d", "postgres"}
		if !preserveOwnership {
			args = append(args, "--no-owner", "--no-privileges")
		}
		args = append(args, "--exit-on-error", remote)
		out, err = sb.exec(append([]string{"pg_restore"}, args...)...)
	case "pg_dump_sql":
		out, err = sb.exec("psql", "-U", "postgres", "-d", "postgres",
			"-v", "ON_ERROR_STOP=1", "-f", remote)
	default:
		return fmt.Errorf("unknown backup format %q", format)
	}
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

// brokenSequencesSQL lists sequences lagging their column's max (surfaces
// only on the first INSERT after a restore).
const brokenSequencesSQL = `
SELECT coalesce(string_agg(seqname, ', '), '') FROM (
  SELECT format('%s.%s', n.nspname, c.relname) AS seqname,
         pg_sequence_last_value(c.oid) AS lastval,
         (xpath('/row/max/text()',
            query_to_xml(format('SELECT max(%I) FROM %I.%I', a.attname, tn.nspname, t.relname),
                         false, true, ''))
         )[1]::text::bigint AS maxval
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_depend d ON d.objid = c.oid AND d.deptype IN ('a','i')
  JOIN pg_class t ON t.oid = d.refobjid
  JOIN pg_namespace tn ON tn.oid = t.relnamespace
  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
  WHERE c.relkind = 'S'
) s WHERE maxval IS NOT NULL AND (lastval IS NULL OR lastval < maxval)`

func runChecks(sb *sandbox, cfg config.Checks) []report.CheckResult {
	var results []report.CheckResult

	if cfg.MinTables > 0 {
		out, err := sb.query(
			"SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')")
		n, _ := strconv.Atoi(out)
		results = append(results, report.CheckResult{
			Name:    fmt.Sprintf("at least %d tables restored", cfg.MinTables),
			Passed:  err == nil && n >= cfg.MinTables,
			Details: "found " + out,
		})
	}

	if cfg.SequenceIntegrity {
		out, err := sb.query(brokenSequencesSQL)
		res := report.CheckResult{Name: "sequence integrity", Passed: err == nil && out == ""}
		switch {
		case err != nil:
			res.Details = "check failed to run: " + firstLine(out)
		case out != "":
			res.Details = "sequences behind their column max: " + out
		}
		results = append(results, res)
	}

	for _, rc := range cfg.RowCounts {
		out, err := sb.queryAs(cfg.VerifyAsRole, "SELECT count(*) FROM "+quoteIdent(rc.Table))
		n, _ := strconv.ParseInt(out, 10, 64)
		res := report.CheckResult{
			Name:   fmt.Sprintf("%s has at least %d rows", rc.Table, rc.Min),
			Passed: err == nil && n >= rc.Min,
		}
		if err != nil {
			res.Details = "query failed: " + firstLine(out)
		} else {
			res.Details = "found " + out
		}
		results = append(results, res)
	}

	for _, q := range cfg.Queries {
		results = append(results, runAssertion(sb, cfg.VerifyAsRole, q))
	}

	return results
}

// runAssertion runs a user-defined SQL assertion against the sandbox, as
// role if set (see queryAs).
func runAssertion(sb *sandbox, role string, q config.Assertion) report.CheckResult {
	out, err := sb.queryAs(role, q.SQL)
	return evaluateAssertion(q.Name, out, err)
}

// evaluateAssertion interprets one assertion query's output. Must be
// exactly one row with a single boolean value.
func evaluateAssertion(name, out string, err error) report.CheckResult {
	res := report.CheckResult{Name: name}
	if err != nil {
		res.Details = "query failed: " + firstLine(out)
		return res
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 1 {
		// Row content omitted: reports can leave the trust boundary.
		res.Details = fmt.Sprintf("query returned %d lines, expected exactly one boolean (check for multiple statements or a trailing semicolon; row content omitted from report)", len(lines))
		return res
	}
	if lines[0] != "t" && lines[0] != "true" {
		res.Details = "query returned a non-boolean value, expected a boolean (value omitted from report)"
		return res
	}
	res.Passed = true
	res.Details = "query returned " + lines[0]
	return res
}

// quoteIdent quotes a possibly schema-qualified identifier.
func quoteIdent(ident string) string {
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		parts[i] = quoteSingleIdent(p)
	}
	return strings.Join(parts, ".")
}

// quoteSingleIdent quotes one identifier, never splitting on ".": for role
// names, which aren't schema-qualified and may legitimately contain a dot.
func quoteSingleIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
