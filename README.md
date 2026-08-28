# restoredrill

**Untested backups aren't backups.** restoredrill proves your PostgreSQL backups actually restore. It fetches the latest backup, restores it into a throwaway Postgres container, runs checks you define, and writes a JSON report with the restore time.

> Status: v0.1.0, early days. Postgres only. Things may still change.

## Why

Everyone knows they should test restores. Almost nobody does, because there's nowhere safe to do it and never enough time. Scripts and cron jobs don't really fix this either, they fail quietly too: the job stops running, or keeps looping on stale data, and nobody gets told.

restoredrill turns the restore drill into one command, and makes it loud if you skip it. It runs on whatever schedule your recovery policy sets, not constantly, since most compliance frameworks actually warn against claiming continuous testing, any gap becomes a finding. You get real proof you did what you said, when you said you'd do it.

A policy doc is easy to fake, on purpose or by accident. Someone can write "we test quarterly" today even if nobody's run a test in a year. A report with a real timestamp, made by the tool itself, is much harder to fake.

If you're doing SOC 2, ISO 27001, or an AWS Foundational Technical Review, this is the kind of proof they want: real logs from real restores, showing what ran and when.

## How this differs

There are other tools in this space.

[Databasus](https://github.com/databasus/databasus) is a solid self-hosted backup platform for Postgres, MySQL, MariaDB, and MongoDB, with a full web UI and restore verification built in. If you want one dashboard managing backups across several database engines, start there. [BackupDrill](https://backupdrill.com) does something close to this for Supabase specifically, including Storage files.

restoredrill does one thing: a CI-native check built for an auditor's report, not a dashboard. Fail-closed by default, an RPO freshness check, your own SQL assertions, RTO tracked against a target, and every field formatted the same way so it copies cleanly into a SOC 2, ISO 27001, or AWS FTR packet. If your backups are handled and you just need proof they work on schedule, this is that.

## Quickstart: ten minutes, no production access

Most people who skip testing restores say there's nowhere safe to do it. A throwaway container on your own laptop works fine.

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

1. **Prechecks**, before the restore even starts: is the backup file big enough, is its archive header readable, and is it actually recent (the RPO check). 
2. **Structural**: did the restore finish, are enough tables there, and are sequences in sync with their tables. 
3. **Read-path**: row counts, data freshness, and any SQL assertions you write. A restore can exit 0 and still be lying until someone actually reads the data. 
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

The JSON report stays inspectable down to what actually ran, nothing polished standing between you and the real logs.

One report can't show you if restores are slowly getting slower over time. That's a job for whatever you feed these reports into, a log tool, a dashboard, even a spreadsheet. Every report includes `restore_duration_seconds` so that's easy to chart.

## Requirements

- Docker
- A PostgreSQL backup: `pg_dump -Fc` archive or plain SQL dump, local or in S3 (`aws` CLI required for S3 sources)

## Alerting: no new dashboard

restoredrill sends results into tools you already watch:

- **Prometheus**: `output.prometheus_textfile` writes node_exporter textfile metrics. Alert on the age of `restoredrill_last_run_timestamp_seconds`. That's your "verified within N hours" signal, and it catches a drill that quietly stopped running.
- **Slack**: `notify.slack_webhook_url` gets a one-line PASS/FAIL summary with the failed checks listed.
- **Anything else**: `notify.webhook_url` gets the full JSON report via POST.

If a notification fails to reach any of these, it shows up in `notify_errors`, and the process exits non-zero too. A notification failing silently is the same problem, just one step further up.

## CI usage

The exit code makes this a natural scheduled CI job. See [`.github/workflows/restoredrill.yml`](.github/workflows/restoredrill.yml) for a working example, or use [`action.yml`](action.yml) directly in your own workflow. Pass `--trigger manual` (and `--triggered-by`) when a person runs it by hand instead. The evidence output looks the same either way.

## Limits

- The ephemeral-container model assumes your database fits comfortably in a container on the runner. Multi-terabyte estates need a different approach (restore to dedicated infra). restoredrill isn't that today.
- `pg_dump`-level verification doesn't exercise PITR or WAL replay. pgBackRest support, which does, is on the roadmap.
- The archive integrity precheck works differently by format. `pg_dump_custom` gets a table-of-contents readability check, but `pg_dump_sql` has no TOC, so it gets a completeness check instead.pg_dump always writes a fixed completion marker at the end of a finished dump, and restoredrill checks for it before restoring. Both are gated by the same `archive_integrity` config flag.
- Picking the right file from an S3 prefix still needs a pattern for plain SQL. `pg_dump_custom` candidates get filtered by their `PGDMP` header during selection. `pg_dump_sql`'s completeness check only runs later, on the one file already chosen for the restore, not during candidate selection. `backup.s3_object_pattern` is required when you combine a prefix source with `pg_dump_sql`. restoredrill fails at config load instead of guessing which file is the real backup.
- We don't cite specific SOC 2 or ISO 27001 clause numbers here. The report is built around what the underlying control actually asks for (documented, provable recovery testing on whatever cadence your policy sets), but getting a compliance citation wrong is worse than not citing one. Check your own control language and auditor instead of trusting a clause number from us.
- The restore runs `pg_restore` with `--no-owner --no-privileges`, so it doesn't fail when the dump references a role that doesn't exist in the throwaway container. That also means role and grant integrity is never actually verified: every check runs as the connecting superuser, so a missing application role, or a grant that was never captured in the backup, won't show up as a failure the way it would in a real recovery. This is a real gap, not fixed yet.

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
