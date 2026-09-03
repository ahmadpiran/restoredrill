# restoredrill

**Untested backups aren't backups.** restoredrill proves your PostgreSQL and MySQL backups actually restore. It fetches the latest backup, restores it into a throwaway container, runs checks you define, and writes a JSON report with the restore time. Or, if something else already restored a copy for you, it can just connect and run the same checks against that instead.

> Status: v0.4.0, early days. Postgres, plus MySQL 8 from `mysqldump` files. Things may still change.

## Why

Everyone knows they should test restores. Almost nobody does, because there is no safe place to test, and there is not enough time. Scripts and cron jobs don't really fix this either, they fail without a warning. The job can stop, or run on stale data, and no one gets a notification.

restoredrill turns the restore drill into one command, and reports it if you skip it. It runs on the schedule that your recovery policy sets. restoredrill does not run continuously, because most compliance frameworks warn against a claim of continuous testing: a gap in testing becomes an audit finding. restoredrill gives you proof that you ran the test, and proof of when you ran it.

A policy document is easy to falsify, by accident or on purpose. A person can write "we test every quarter" even when no test ran in the last year. A report with a real timestamp is harder to falsify. restoredrill generates this report, not a person.

If you're doing SOC 2, ISO 27001, or an AWS Foundational Technical Review, this is the kind of proof they want: real logs from real restores, showing what ran and when.

## How restoredrill differs from other tools

Other tools exist for this purpose.

[Databasus](https://github.com/databasus/databasus) is a solid self-hosted backup platform for Postgres, MySQL, MariaDB, and MongoDB, with a full web UI and restore verification built in. If you want one dashboard managing backups across several database engines, start there. [BackupDrill](https://backupdrill.com) does something close to this for Supabase specifically, including Storage files.

restoredrill does one thing: a CI-native check for an auditor's report, not a dashboard. It fails closed by default. restoredrill includes an RPO freshness check, runs your own SQL assertions, tracks the RTO against a target, and every field uses the same format, so you can copy the report directly into a SOC 2, ISO 27001, or AWS FTR packet. Use restoredrill if your backup process already works and you need proof that it works on schedule.

## Quickstart: 10 minutes, no production access

Most people who skip testing restores say there's nowhere safe to do it. A throwaway container on your own laptop works fine.

1. Create a dump of any PostgreSQL database. The source can be Supabase, RDS, or your local development database:

   ```
   pg_dump -Fc -d "$DATABASE_URL" -f backup.dump
   ```

2. Copy [`examples/quickstart.yml`](examples/quickstart.yml) to the same directory. Or set `backup.source` to the path where you saved the dump.

3. Run the command:

   ```
   $ restoredrill --config quickstart.yml --trigger manual
   restoredrill: PASS, restore took 4.2s, 1/1 checks passed, report: restoredrill-report.json
   ```

That's it. No S3, no CI, no production credentials. You now have a JSON file proving a real restore happened, timestamped, on your laptop, in about ten minutes. Once that works, add real checks (row counts, freshness, your own SQL assertions, see [`examples/restoredrill.yml`](examples/restoredrill.yml)) and point it at your real backup.

## What restoredrill checks

Checks run in tiers. Every check fails closed. If a check can't run, that counts as a failure, not a skip.

1. **Prechecks**, before the restore even starts: is the backup file big enough, is its archive header readable, and is it actually recent (the RPO check). 
2. **Structural**: did the restore finish, are enough tables there, and are sequences in sync with their tables. On MySQL, whether any view, trigger, event or routine came back with a `DEFINER` account that doesn't exist. 
3. **Read-path**: row counts, data freshness, and any SQL assertions you write. A restore can exit 0 and still be lying until someone actually reads the data. 
4. **RTO evidence**: how long the restore actually took, checked against a target if you set one.
5. **Environment sanity**: the container has to come up and accept connections at all, which also proves the recovery environment has enough room to work.

`backup.format: mysqldump_sql` runs every tier, with two differences. `definer_integrity` replaces `sequence_integrity`, because sequences are a PostgreSQL concept. `verify_as_role` does not work for this format (see Limits). restoredrill rejects both options at config load, instead of accepting them and doing nothing.

`backup.format: existing_connection` skips tiers 1, 4, and 5. This format has no backup file to check, no restore to time, and no container of its own to start. Instead, this format runs one connectivity precheck, then runs tiers 2 and 3 the same way as every other format.

## The evidence report

The report is the real product here. Automating the restore is the easy part. Getting a report format an auditor accepts on the first pass takes real iteration. This schema comes from someone who paid that cost directly: three rewrites and months of back and forth with a real auditor.

Every field is always present in the report. A field is never absent because it does not apply to the current drill. Auditors often copy report fields into a spreadsheet, and a field that is sometimes present and sometimes absent breaks that process. Key fields:

- `triggered_by`, `triggered_by_user`, and `pipeline_job_id`: these fields use the same schema for a scheduled run and a manual run (`--trigger manual --triggered-by you@example.com`). A manual run carries the same accountability as a scheduled run.
- `backup_resolved_key`: the actual file or object that restoredrill used, not only the configured source. If your source is an S3 prefix, restoredrill selects the newest object that matches the expected backup format. A checksum file or other sidecar file cannot win this selection only because it is newer.
- `backup_candidates_considered`: every S3 prefix object that restoredrill checked, in order, with the reason for each skip. This field is empty for a source that is not a prefix.
- `backup_timestamp`, `backup_age_seconds`, `rpo_target_seconds`, and `rpo_met`: the freshness and RPO evidence from tier 1.
- `restore_initiated_at`, `restore_completed_at`, `restore_duration_seconds`, `rto_target_seconds`, and `rto_met`: the RTO evidence, measured against a target if you set one.
- `restore_method`: the `backup.format` value that produced the checked database. Without this field, every report implied that restoredrill restored the backup and then checked it. For `existing_connection` mode (see below), this is not true. This field states the actual situation, so a reader does not mistake `restore_duration_seconds: 0` and an empty backup source for a defect.
- `validation_errors`: every failed check, in its own field, with the failure and the reason. When a run fails, you need to know what failed, not only that a failure occurred.
- `notify_errors`: a broken Slack or webhook URL is a finding, not a silent no-op. If a notification fails to deliver, this field records the failure. The process then exits with a non-zero code, even when the drill itself passed.
- Every timestamp uses the literal string format `"YYYY-MM-DD HH:MM:SS UTC"`, not an epoch value or RFC3339 format. Most auditor workflows end with a copy into a spreadsheet, and this format survives that copy.

The JSON report stays inspectable down to the actual events that ran. No polished layer sits between you and the real log data.

One report can't show you if restores are slowly getting slower over time. That's a job for whatever you feed these reports into, a log tool, a dashboard, even a spreadsheet. Every report includes `restore_duration_seconds` so that's easy to chart.

## Requirements

- Docker (not needed for `backup.format: existing_connection`, see below)
- A PostgreSQL backup: a `pg_dump -Fc` archive or a plain SQL dump, on the local disk or in S3 (the `aws` CLI is required for an S3 source); or a pgbackrest repository (see [`examples/pgbackrest.yml`](examples/pgbackrest.yml)); or an already-restored database that another tool produced (`psql` is required on the host; see [`examples/existing-connection.yml`](examples/existing-connection.yml))
- Or a MySQL backup: a plain SQL `mysqldump` file, on the local disk or in S3 (see [`examples/mysql.yml`](examples/mysql.yml))

## Alerting: no new dashboard

restoredrill sends results to tools that you already monitor:

- **Prometheus**: `output.prometheus_textfile` writes node_exporter textfile metrics. Set an alert on the age of `restoredrill_last_run_timestamp_seconds`. This gives you a "verified within N hours" signal, and it catches a drill that stopped running without a warning.
- **Slack**: `notify.slack_webhook_url` receives a one-line PASS/FAIL summary. This summary lists the failed checks.
- **Any other tool**: `notify.webhook_url` receives the full JSON report through an HTTP POST request.

If a notification fails to reach its destination, `notify_errors` records this, and the process exits with a non-zero code. A silent notification failure is the same problem, one step further from the source.

## CI usage

The exit code makes restoredrill a good fit for a scheduled CI job. See [`.github/workflows/restoredrill.yml`](.github/workflows/restoredrill.yml) for a working example, or use [`action.yml`](action.yml) directly in your own workflow. When a person runs restoredrill by hand, pass `--trigger manual` and `--triggered-by`. The evidence output has the same format in both cases.

## Limits

- The ephemeral-container model assumes your database fits in a container on the runner. A multi-terabyte database needs a different approach, such as a restore to dedicated infrastructure. restoredrill does not support this today.
- pgbackrest support restores the latest backup in a stanza only; there's no target-time selection for point-in-time recovery yet. `restore_duration_seconds` for this format covers the whole physical recovery: `pgbackrest restore` plus Postgres startup plus WAL replay until recovery completes, not just the copy step.
- restoredrill does not build or distribute a PostgreSQL image with pgbackrest installed. The image at `postgres.image` must have both PostgreSQL and pgbackrest installed; see [`examples/pgbackrest.yml`](examples/pgbackrest.yml).
- The archive integrity precheck differs by format. `pg_dump_custom` gets a table-of-contents readability check. `pg_dump_sql` has no table of contents, so it gets a completeness check instead: `pg_dump` always writes a fixed completion marker at the end of a finished dump, and restoredrill checks for this marker before the restore. The `archive_integrity` config flag controls both checks. `mysqldump_sql` gets the same type of completeness check, against the `-- Dump completed` marker that `mysqldump` writes at the end of a finished dump. A dump made with the `--compact` or `--skip-comments` option has no marker; set `archive_integrity: false` for this type of dump.
- The selection of the correct file from an S3 prefix still needs a pattern for a plain SQL format. restoredrill filters `pg_dump_custom` candidates by their `PGDMP` header during selection. The `pg_dump_sql` completeness check runs later, only on the one file already selected for the restore, not during candidate selection. `backup.s3_object_pattern` is required when you combine a prefix source with `pg_dump_sql` or `mysqldump_sql`. restoredrill fails at config load, instead of a guess at the real backup file.
- MySQL support covers only MySQL 8 and a plain `mysqldump` SQL file. restoredrill does not support MariaDB, a compressed dump, a physical backup tool such as XtraBackup, or point-in-time recovery. Every check runs against the single database named in `mysql.database`. A dump from a database with a different name fails with a clear error, not a partial pass.
- `checks.verify_as_role` is not available for MySQL, and restoredrill rejects this option at config load. MySQL's `SET ROLE` command activates only a role already granted to the connecting account, and it does not remove the privileges that the account already holds. A check would still run with full rights, while the report claimed a role check. A check for a missing grant, in the same way as the PostgreSQL path, needs a connection as the application account with its own password. restoredrill does not support this connection method yet.
- restoredrill does not yet check for AUTO_INCREMENT drift, so MySQL has no equivalent of the PostgreSQL sequence integrity check. `information_schema` reports only an approximate next value for an InnoDB table, and a comparison against the real maximum value in a table needs dynamic SQL for each table.
- We don't cite specific SOC 2 or ISO 27001 clause numbers here. The report is built around what the underlying control actually asks for (documented, provable recovery testing on whatever cadence your policy sets), but getting a compliance citation wrong is worse than not citing one. Check your own control language and auditor instead of trusting a clause number from us.
- By default, checks run as the sandbox superuser, so a missing application role or grant won't show up as a failure the way it would in a real recovery. Set `backup.globals_source` (a `pg_dumpall --globals-only` file) and `checks.verify_as_role` to restore real roles and grants, and check them as that role instead. See [`examples/restoredrill.yml`](examples/restoredrill.yml).
- `backup.format: existing_connection` trusts the connection that you provide. restoredrill verifies only the data that it can see. It cannot confirm that the earlier restore process, which produced this database, ran correctly. The session enforces read-only access at the PostgreSQL level (`default_transaction_read_only=on`), so a check cannot write to a database that restoredrill does not own. This restriction does not detect a wrong database or a stale database, if the original restore process used the wrong source.

## Roadmap

- pgBackRest point-in-time target selection (version 1 restores only the latest backup)
- Support for GCS as a backup source
- Support for restic as a backup source
- MySQL AUTO_INCREMENT drift checks, compressed dump support, and MariaDB support
- Differential checks between the restored database and production, over the pre-backup window
- A scheduled mode

## Building

```
go build ./cmd/restoredrill
```

## Testing

```
go test ./...
```

Several checks have Docker-backed integration tests. These tests start real PostgreSQL and MySQL containers. The pgbackrest test builds a real stanza and a real backup. The MySQL test creates a real `mysqldump` file from a seeded source database, then restores it. Each test skips without an error when Docker is not available. The `existing_connection` tests also need `psql` on the host, because this mode uses `psql` to connect; these tests also skip without an error when `psql` is not available.

## License

[MIT](LICENSE)
