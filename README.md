# restoredrill

**Untested backups aren't backups.** restoredrill proves your PostgreSQL backups actually restore. It fetches the latest backup, restores it into a throwaway Postgres container, runs checks you define, and writes a JSON report with the restore time.

> Status: v0.1.0, early days. Postgres only. Things may still change.

## Why

Everyone knows they should test restores. Almost nobody does, because there's nowhere safe to restore to and never enough time. Teams that automate it usually hand-roll a cron job and a script, and those fail quietly in their own way. The real danger isn't a bad backup. It's the drill silently not running, or silently restoring a stale file, and nobody noticing for a month.

restoredrill makes the drill a one-command habit, and makes skipping it loud. It runs on whatever schedule your recovery policy sets. It doesn't assume you need to test constantly (a lot of GRC advice actually warns against that, since any gap in a "continuous" claim becomes an audit finding). It just proves you did what you said you'd do, on schedule.

A policy doc is easy to fake, on purpose or by accident. "We test quarterly" could have been written last week with nothing actually run in a year. A timestamped, machine-generated report is harder to fake.

If you're doing SOC 2, ISO 27001, or an AWS Foundational Technical Review, this is the shape of evidence they ask for: real logs from real restores, tied to what ran and when.

## Quickstart: ten minutes, no production access

The usual excuse for not testing restores is "there's nowhere safe to do it." There is: a throwaway container on your own laptop.

1. Dump whatever Postgres you have. Supabase, RDS, your local dev box, doesn't matter:

   ```
   pg_dump -Fc -d "$DATABASE_URL" -f backup.dump
   ```

2. Copy [`examples/quickstart.yml`](examples/quickstart.yml) next to it (or point `backup.source` at wherever you saved it).

3. Run it:

   ```
   $ restoredrill --config quickstart.yml --trigger manual
   restoredrill: PASS, restore took 4.2s, 1/1 checks passed, report: restoredrill-report.json
   ```

That's it. No S3, no CI, no production credentials. You now have a JSON file proving a real restore happened, timestamped, on your laptop, in about ten minutes. Once that works, add real checks (row counts, freshness, your own SQL assertions, see [`examples/restoredrill.yml`](examples/restoredrill.yml)) and point it at your real backup.

## What it checks

Checks run in tiers. Every check is fail-closed: if a check can't run, that counts as a failure, not a skip.

1. **Prechecks**, before the restore even starts: is the backup file big enough, is its archive header readable, and is it actually recent (the RPO check). This catches a backup cron that died quietly and left the same stale file in place.
2. **Structural**: did the restore finish, are enough tables there, and are sequences in sync with their tables. A sequence that lags its column's max value only shows up on the first INSERT after a real disaster. restoredrill catches it now instead.
3. **Read-path**: row counts, data freshness, and any SQL assertions you write. A restore can exit 0 and still be lying until someone actually reads the data. One real incident: a restore process exited clean while the database behind it was silently corrupted. That's what these checks are for.
4. **RTO evidence**: how long the restore actually took, checked against a target if you set one.
5. **Environment sanity**: the container has to come up and accept connections at all, which also proves the recovery environment has enough room to work.

## The evidence report

The report is the real product here. Automating the restore is the easy part. Getting a report format an auditor accepts on the first pass takes real iteration. This schema comes from someone who paid that cost directly: three rewrites and months of back and forth with a real auditor.

Every field is always present, never missing just because it doesn't apply. Auditors often copy these into a spreadsheet, and a field that sometimes exists and sometimes doesn't breaks that. Key fields:

- `triggered_by` / `triggered_by_user` / `pipeline_job_id`: same schema whether a scheduler ran this or a person pushed the button (`--trigger manual --triggered-by you@example.com`). Manual runs carry the same accountability as scheduled ones.
- `backup_resolved_key`: the actual file or object drilled, not just the configured source. If your source is an S3 prefix, restoredrill picks the newest object, but only after checking it actually looks like the right backup format. A checksum file or other sidecar uploaded after the real backup can't win just by being newer.
- `backup_candidates_considered`: every S3 prefix object restoredrill looked at, in order, and why any got skipped. Empty for non-prefix sources.
- `backup_timestamp` / `backup_age_seconds` / `rpo_target_seconds` / `rpo_met`: the freshness and RPO evidence described above.
- `restore_initiated_at` / `restore_completed_at` / `restore_duration_seconds` / `rto_target_seconds` / `rto_met`: RTO evidence, measured against a target if you set one.
- `validation_errors`: every failed check, as its own field, with what failed and why. If a run fails, you want to know what broke, not just that something did.
- `notify_errors`: a broken Slack or webhook URL is a finding, not a silent no-op. If a notify sink fails to deliver, it shows up here, and the process exits non-zero, even if the drill itself passed.
- All timestamps are a literal `"YYYY-MM-DD HH:MM:SS UTC"` string, not epoch or RFC3339. Most auditor workflows end in copy-pasting into a spreadsheet, and this format survives that.

The JSON report stays inspectable down to what actually ran. No polished PDF summary asking you, or your auditor, to just trust it.

Whether your restore is getting slower over time isn't something one report can show on its own. That's a job for whatever you feed these reports into: a log aggregator, a dashboard, even a spreadsheet. Every report includes `restore_duration_seconds` so that's easy to chart.

## Requirements

- Docker
- A PostgreSQL backup: `pg_dump -Fc` archive or plain SQL dump, local or in S3 (`aws` CLI required for S3 sources)

## Alerting: no new dashboard

restoredrill sends results into what you already watch, instead of asking you to check somewhere new:

- **Prometheus**: `output.prometheus_textfile` writes node_exporter textfile metrics. Alert on the age of `restoredrill_last_run_timestamp_seconds`. That's your "verified within N hours" signal, and it catches a drill that quietly stopped running.
- **Slack**: `notify.slack_webhook_url` gets a one-line PASS/FAIL summary with the failed checks listed.
- **Anything else**: `notify.webhook_url` gets the full JSON report via POST.

A failed delivery to any of these shows up in `notify_errors` and makes the process exit non-zero. Losing your only notification channel silently is exactly the kind of quiet failure this tool exists to catch.

## CI usage

The exit code makes this a natural scheduled CI job. See [`.github/workflows/restoredrill.yml`](.github/workflows/restoredrill.yml) for a working example, or use [`action.yml`](action.yml) directly in your own workflow. Pass `--trigger manual` (and `--triggered-by`) when a person runs it by hand instead. The evidence output looks the same either way.

## Honest limits

- The ephemeral-container model assumes your database fits comfortably in a container on the runner. Multi-terabyte estates need a different approach (restore to dedicated infra). restoredrill isn't that today.
- `pg_dump`-level verification doesn't exercise PITR or WAL replay. pgBackRest support, which does, is on the roadmap.
- The archive integrity precheck only works for `pg_dump_custom`. Plain SQL dumps have no table-of-contents header to check, so there's no equivalent precheck for that format. That's a real gap, not an oversight: `pg_dump_sql` corruption gets caught later, and more expensively, at restore time instead.
- The same gap applies to picking the right file from an S3 prefix. restoredrill can check a candidate's content for `pg_dump_custom` (it looks for the `PGDMP` header), but plain SQL has no such signature. `backup.s3_object_pattern` is required when you combine a prefix source with `pg_dump_sql`. restoredrill fails at config load instead of guessing which file is the real backup.
- We don't cite specific SOC 2 or ISO 27001 clause numbers here. The report is built around what the underlying control actually asks for (documented, provable recovery testing on whatever cadence your policy sets), but getting a compliance citation wrong is worse than not citing one. Check your own control language and auditor instead of trusting a clause number from us.

## Roadmap

- pgBackRest repositories (PITR path verification)
- GCS backup sources
- MySQL, then restic
- Differential checks (restored vs. prod over the pre-backup window)
- Scheduled mode

## Building

```
go build ./cmd/restoredrill
```

## Testing

```
go test ./...
```

The sequence-integrity check has a Docker-backed integration test (it starts a real Postgres container). It skips cleanly if Docker isn't available.

## License

[MIT](LICENSE)
