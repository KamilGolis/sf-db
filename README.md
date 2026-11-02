# sfdb

Clone Salesforce objects into a local SQLite database and open it with IntelliJ's Database tool.

This is a standalone Go CLI. It does not require an IntelliJ plugin: the IDE already has a SQLite
driver, and SQLite executes the SQL, so joins, filters, schema browsing, and the data grid work
without a Salesforce-specific JDBC driver.

Status: **MVP**. One-shot clone works. Incremental refresh, deletes, and write-back are not
implemented yet.

## Requirements

- Go 1.27 or newer to build.
- The Salesforce CLI (`sf`) on `PATH`, already authenticated to the org you want to clone. `sf` is
  used once per run to bootstrap the access token and instance URL; describes and queries go straight
  to the Salesforce REST/Bulk API.
- IntelliJ IDEA Ultimate, WebStorm, DataGrip, or another JetBrains IDE with Database Tools.

## Build

```bash
go build -o bin/sfdb ./cmd/sfdb
```

Or run it directly:

```bash
go run ./cmd/sfdb --help
```

## Usage

Print the DDL without touching a database:

```bash
sfdb schema --org my-org --objects Account,Contact,Opportunity --workers 4
```

Clone objects into a SQLite file:

```bash
sfdb clone \
  --org my-org \
  --objects Account,Contact,Opportunity \
  --out clone.sqlite \
  --workers 4
```

Clone every custom object:

```bash
sfdb clone --org my-org --custom-objects --out custom.sqlite --workers 4
```

Preview a large selection before querying anything:

```bash
sfdb clone --org my-org --all-objects --dry-run
```

Flags that matter:

| Flag | Default | Meaning |
|---|---|---|
| `--objects` | none | explicit object names; unions with selectors |
| `--all-objects` | off | add every queryable standard and custom object |
| `--custom-objects` | off | add every queryable custom object |
| `--standard-objects` | off | add every queryable standard object |
| `--include-system-objects` | off | include History, Share, Feed, Tag, and ChangeEvent objects |
| `--dry-run` | off | print the resolved object set and exit |
| `--strict` | off | abort on the first failure, including selector-derived objects |
| `--workers` | `4` | parallel objects for describe and load (1-32) |
| `--engine` | `auto` | `auto`, `rest`, or `bulk` |
| `--bulk-threshold` | `50000` | `auto` uses Bulk above this record count |
| `--all-rows` | off | include soft-deleted records (`queryAll`) |
| `--force` | off | replace an existing output file |
| `--sf-bin` | `sf` | path to the sf CLI used for auth bootstrap |
| `--verbose` | off | show structured logs instead of the progress display |

### Progress

By default the CLI shows a changing status line and progress bars on stderr: credential bootstrap,
object listing, describe, schema creation, load, indexes, and publish. On a terminal the line
updates in place. When stderr is redirected, it prints a plain line at each 10% step instead.

`--verbose` replaces the progress display with structured `slog` output, including per-object debug
detail. Errors are printed in either mode; skipped objects are listed in the final summary.

### Object selection

Selector flags resolve through Describe Global, using its `custom` boolean (not a `__c` suffix
guess). They keep only queryable, non-deprecated objects and, by default, exclude derived system
objects (`*History`, `*Share`, `*Feed`, `*Tag`, `*ChangeEvent`). `--include-system-objects` restores
the literal set.

`--objects` unions with selectors. `--custom-objects` and `--standard-objects` combine;
`--all-objects` is shorthand for both and conflicts with either category flag. Names are
deduplicated case-insensitively and sorted alphabetically.

On the DMS-DEV org (2026-11-03): `--all-objects` resolves to **721** objects (705 standard, 16
custom); `--all-objects --include-system-objects` resolves to **1,042**.

Selector-derived describe or query failures are skipped with a warning and a `skipped N object(s)`
summary. Explicit `--objects` failures abort; `--strict` makes every failure abort.

Then open `clone.sqlite` in the IDE: **File | New | Data Source | SQLite**. The first connection
downloads the Xerial SQLite driver; after that the file opens like any other SQLite database.

`refresh` is a stub. Re-run `clone --force` for now.

## How it works

1. `sf org auth show-access-token` and `sf org display` run once, in parallel, for the token and
   instance URL. This is the only hot-path use of `sf`.
2. Describes go to `GET /services/data/vXX.X/sobjects/{Object}/describe` in parallel.
3. Records go to `GET /query` + `nextRecordsUrl` for normal objects. In `auto` mode, an object above
   `--bulk-threshold` records uses a Bulk API 2.0 query job instead.
4. Rows are written by one SQLite writer. Indexes are created after the load.
5. A 401 refreshes the token once by re-running the bootstrap.

## What it creates

- One table per Salesforce object, named with the API name and quoted (`"Account"`, `"MyObject__c"`).
- `"Id" TEXT PRIMARY KEY NOT NULL`, plus every non-compound field from describe.
- `"IsDeleted"` and `"SystemModstamp"` when the object does not already have them.
- Foreign keys only for unambiguous references whose target is in the object set. Polymorphic
  references (`OwnerId`, `WhoId`, `WhatId`) stay plain `TEXT`.
- Indexes on `SystemModstamp` and every reference column, created after the load.
- Sidecar tables: `sf_meta_object`, `sf_meta_field`, `sf_meta_sync`.

Compound fields (`BillingAddress`, `Location`) are not columns; their component fields are.
Formula and roll-up fields are materialized from the query.

## Type mapping

| Salesforce | SQLite |
|---|---|
| `id`, `string`, `textarea`, `email`, `phone`, `url`, `picklist`, `multipicklist`, `combobox`, `reference`, `encryptedstring`, `json` | `TEXT` |
| `boolean`, `int`, `long` | `INTEGER` |
| `double`, `percent` | `REAL` |
| `currency` | `NUMERIC` |
| `date`, `datetime`, `time` | `TEXT` (ISO 8601) |
| `base64` | `TEXT` (base64 payload) |

SQLite type affinity does not enforce these; the IDE uses them as hints.

## Limitations

- **Token bootstrap costs ~8-15 s per run.** It is once per clone, not per object. A token cache is
  intentionally not written to disk.
- **Snapshot, not live data.** Re-run the clone to refresh.
- **REST preserves NULL vs empty string; Bulk CSV does not.** Bulk turns both into an empty value.
- **No incremental refresh or deletes.** A fresh clone is complete.
- **No attachments or binary payloads.** `base64` fields are stored as text.
- **Describes are not cached on disk yet.** They are fetched once per run, in parallel.
- **Windows file lock.** An open SQLite connection in the IDE blocks replacing the file. The CLI
  writes `clone.sqlite.tmp` and renames it at the end, which is safe for a new file.
- **Bulk API limits apply.** Very large objects may need a longer wait or a resumed job.

## Layout

```text
cmd/sfdb/              CLI entry point
internal/cli/          cobra commands and composition
internal/salesforce/   auth bootstrap, REST/Bulk client, JSON models
internal/schema/       domain model, type map, DDL
internal/pipeline/     describe -> query -> load orchestration, worker pool
internal/store/        SQLite schema, CSV/row loaders, metadata
```

The package direction is `cli -> pipeline -> schema`, with `salesforce` and `store` as adapters.
The domain (`schema`) has no I/O.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

The Salesforce client tests use `httptest`; the store and pipeline tests use a temporary SQLite file.
No org is required for the test suite.
