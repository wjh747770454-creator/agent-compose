# Sandbox Summary Index

This document describes a queryable summary index for sandboxes. Today
`Store.ListSandboxes` (`pkg/storage/sessionstore/store.go`) answers every list
request by scanning `SandboxRoot`: it reads every sandbox directory, opens and
JSON-decodes each `metadata.json`, filters and sorts the whole set in memory,
and only then paginates. The cost is `O(N)` file opens plus `O(N)` JSON decodes
on **every** call, and `limit` saves no I/O because pagination happens after the
full load. Sandbox count grows without bound, so this cost grows linearly over
time.

This design adds a SQLite summary index so list, filter, sort, and pagination
run against an index instead of the filesystem, turning each list into an
`O(page)` query. Sandbox directories and `metadata.json` remain the source of
truth; the index is a derived, always-rebuildable cache.

## Design Goals

- `ListSandboxes` filters, sorts, and paginates with a single indexed query over
  all sandboxes, then reads only the resulting page from disk.
- List cost is bounded by page size, not by total sandbox count.
- The index never becomes a second source of truth: it can be deleted and
  rebuilt from the filesystem at any time without data loss.
- Introducing the index changes no external contract: the `ListSandboxes` gRPC
  request/response, the opaque cursor format, and `GetSandbox` behavior all stay
  identical.

## Non-Goals

- **Retention / disk GC.** Unbounded sandbox growth also grows disk usage. That
  is a separate concern. This design only guarantees that when a sandbox is
  removed, its index row is removed too; it does not decide *when* sandboxes
  should be garbage-collected.
- **Moving sandbox data into SQLite.** `metadata.json`, workspace bytes, cells,
  and events stay on the filesystem. Only a summary is mirrored.
- **Historical archive.** The index mirrors current filesystem state. When a
  sandbox directory is gone, its index row is gone; the index keeps no history.
- **ORM / query-builder adoption.** The index is one table with simple SQL,
  written directly against `database/sql`.

## Core Principle

The filesystem is authoritative; the index is a rebuildable cache. Three rules
follow and govern every decision below:

1. **Filesystem authoritative, index is cache.** If the index drifts or is
   corrupted, rescanning `SandboxRoot` fully repairs it. No sandbox data is ever
   at risk.
2. **Index co-located with its data.** The index lives inside `SandboxRoot`, so
   wiping `SandboxRoot` removes the index with it — no orphaned state in an
   unrelated file, no cross-file lifecycle mismatch.
3. **Cache needs no migrations.** When the index schema changes, the table is
   dropped and rebuilt from the filesystem under the new schema. The index does
   not participate in any schema-version/migration discipline.

## Index Database

### Location and ownership

A dedicated SQLite database at `SandboxRoot/index.db`, owned by
`sessionstore.Store`. It is separate from the daemon config database
(`data.db`). Keeping it separate is deliberate: the index is a disposable cache
whose lifecycle matches `SandboxRoot`, and a separate file lets it be dropped
and rebuilt (rule 3) without touching config data.

### Schema

```sql
CREATE TABLE sandboxes (
    id             TEXT PRIMARY KEY,
    short_id       TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL DEFAULT '',
    trigger_source TEXT NOT NULL DEFAULT '',
    driver         TEXT NOT NULL DEFAULT '',
    vm_status      TEXT NOT NULL DEFAULT '',
    workspace_path TEXT NOT NULL DEFAULT '',
    workspace_id   TEXT NOT NULL DEFAULT '',
    workspace_name TEXT NOT NULL DEFAULT '',
    workspace_type TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_sandboxes_updated
    ON sandboxes(updated_at DESC, id DESC);

CREATE INDEX idx_sandboxes_vm_status_updated
    ON sandboxes(vm_status, updated_at DESC, id DESC);
```

Every field consumed by `SandboxMatchesListOptions`
(`pkg/model/session_list.go`) and the list sort key is materialized, so a list
query never has to open a sandbox directory. Exact-match (`driver`), range
(`created_at`, `updated_at`), sort, and keyset all use `idx_sandboxes_updated`;
status-scoped lists use `idx_sandboxes_vm_status_updated`. Substring filters
(`trigger_source`, `title`, `workspace_*`) run as `LIKE '%x%'` column scans over
`N` rows — not index-assisted, but orders of magnitude cheaper than `N` file
opens plus JSON decodes.

### Schema version

The index records its own structure version, via `PRAGMA user_version`. On
startup the store compares it against the version compiled into the code. On a
mismatch (or an absent/damaged `index.db`), the store drops the table and
triggers a full rebuild (see [Backfill and Rebuild](#backfill-and-rebuild)).
This is how "cache needs no migrations" is realized: schema evolution is a
drop-and-rebuild, never an `ALTER`.

## Write Path

`sessionstore.Store` holds a narrow recorder interface so the store is not
coupled to the index implementation:

```go
type SandboxIndex interface {
    Upsert(ctx context.Context, sandbox *domain.Sandbox) error
    Delete(ctx context.Context, id string) error
}
```

The store projects an index row from an already-loaded `*Sandbox` summary in one
place (`indexRow(sb)`), so the column list has a single definition and cannot
drift across call sites.

The recorder is invoked write-through at every mutation:

| Store mutation | Index action |
| --- | --- |
| `createSandboxWithOptions` (after `metadata.json` is written) | `Upsert` |
| `UpdateSandbox` / `saveSandboxPreservingCounts` | `Upsert` |
| `AddEvent` / other summary-count changes | `Upsert` |
| `RemoveSandbox` (after `os.RemoveAll`) | `Delete` |

### Conflict resolution

Upserts are last-writer-wins keyed on `updated_at`, so a concurrent background
rebuild cannot overwrite a fresh row with a stale one:

```sql
INSERT INTO sandboxes (...) VALUES (...)
ON CONFLICT(id) DO UPDATE SET ...
WHERE excluded.updated_at >= sandboxes.updated_at;
```

### Error policy

The filesystem write is authoritative and happens first. If the subsequent
index write fails, the sandbox mutation still succeeded on disk. Index write
failures are therefore logged at `warn` and do not fail the operation; the next
reconcile repairs the row. `Delete` failures are likewise non-fatal — a lingering
row is pruned later (see [Orphan Pruning](#orphan-pruning)).

## Read Path

### ListSandboxes

`ListSandboxes` is rewritten as a single SQL statement. `SandboxListOptions`
translates directly to SQL:

| Option | SQL |
| --- | --- |
| `Driver` | `driver = ?` |
| `SandboxType` | derived from `trigger_source`; `trigger_source` predicate |
| `TriggerSourceQuery` / `TitleQuery` / `WorkspaceQuery` | `LIKE '%x%'` |
| `VMStatus` | `vm_status = ?` |
| `CreatedFrom/To`, `UpdatedFrom/To` | `created_at`/`updated_at` range |
| `BeforeUpdatedAt`, `BeforeID` (cursor) | `(updated_at, id) < (?, ?)` |
| sort | `ORDER BY updated_at DESC, id DESC` |
| `Limit` | `LIMIT ?` |

The indexed query filters, orders, and paginates over all sandboxes touching
only one page of rows. The list response, however, surfaces fields the lean
index does not store (guest image, cell/event counts, tags, workspace
reclamation state), so each row in the page is then loaded from its
`metadata.json` for full fidelity. This keeps the expensive part — scanning and
sorting the whole set — in SQLite, while disk reads are bounded by page size
rather than total count. The index carries exactly the columns needed to filter
and sort; full detail comes from the page's files.

### GetSandbox

`GetSandbox` is unchanged: it reads `metadata.json` for full detail. The index
serves *lists*, not *detail*. This keeps the index narrow and keeps full sandbox
state on the filesystem.

### External contract

The `ListSandboxes` handler (`pkg/agentcompose/api/sandbox.go`), the opaque
cursor encoding (`encodeSandboxCursor` / `decodeSandboxCursor`, which already
encode `(UpdatedAt, SandboxID)`), and the proto messages
(`ListSandboxesRequest{limit, cursor}` / `ListSandboxesResponse{sandboxes,
next_cursor}`) are all untouched. The API path is already keyset-based; this
change only makes the store execute that keyset efficiently. In-flight cursors
issued before the change remain valid because they carry exactly the
`(updated_at, id)` pair the new `WHERE` clause consumes.

## Backfill and Rebuild

### Trigger

A full rebuild runs when `index.db` is absent, or when `PRAGMA user_version`
does not match the code's index version. Normal restarts, where `index.db` is
already populated and write-through has kept it current, do **not** rebuild.

### Background, asynchronous

1. On startup the store synchronously creates the (empty) table structure and
   sets an in-process `rebuilding` flag. The daemon is immediately available;
   startup does not block on a filesystem scan.
2. A background goroutine walks `SandboxRoot`, loading each sandbox and issuing
   `Upsert`. This is the only `O(N)` full scan, and it happens only on a cold
   rebuild.
3. When the walk completes, the `rebuilding` flag clears.

This keeps startup time independent of sandbox count, which matters precisely
because sandbox count grows without bound.

### Coexistence with write-through

The background walk and foreground write-through both use the conflict-resolving
upsert, so whichever writes last with the newer `updated_at` wins; neither
clobbers the other with stale data. A sandbox created during a rebuild is
recorded immediately by write-through and does not depend on the walk reaching
it. A sandbox removed during a rebuild is `Delete`d and its directory is gone, so
the walk cannot resurrect it.

### Rebuilding visibility

While `rebuilding` is set, a list may be incomplete (it reflects rows walked so
far plus any written through). `SandboxListResult` carries an `IndexRebuilding
bool` so the API and UI can indicate that the index is still warming up. This
temporary incompleteness is the accepted cost of not blocking startup.

## Orphan Pruning

A directory can disappear without going through `RemoveSandbox` — a manual
`rm`, a crash mid-operation, or any cleanup path that does not route through the
store. The index must not keep such ghost rows. Three mechanisms cover this:

1. **Normal removal.** Garbage collection routes through
   `RemovalCoordinator.Prune` → `Remove` → `Store.RemoveSandbox`
   (`pkg/sessions/removal.go`), which triggers `Delete`. This is the common path
   and needs no extra work.
2. **Lazy pruning.** When any operation loads a sandbox's `metadata.json` and
   gets `os.IsNotExist`, it issues `Delete` for that id, so a ghost row is
   removed the next time it is touched.
3. **Reconcile on rebuild.** A full rebuild reconciles both directions: rows
   whose directory no longer exists are deleted, in addition to upserting
   existing directories.

## Consistency Model

The index is an eventually-consistent read cache, not a source of truth. Under
normal operation write-through keeps it strongly consistent with the
filesystem. Under abnormal conditions — a crash mid-write, an out-of-band
directory change, or a schema-version bump — the index self-heals via lazy
pruning and full rebuild. Deleting `index.db` at any time is a safe operation; it
forces a rebuild on next startup.

## Testing Strategy

- **List equivalence.** Given a filesystem fixture of sandboxes, the SQL-backed
  `ListSandboxes` returns results equivalent to the previous full-scan
  implementation across every `SandboxListOptions` field: driver, status,
  substring queries, time ranges, keyset pagination, and ordering.
- **Write-through.** Create / update / remove / event-count changes are
  reflected in the corresponding index row.
- **Rebuild.** Given `N` sandbox directories and no `index.db`, the background
  rebuild yields a complete index; while `rebuilding` is set, lists are
  partial and `IndexRebuilding` is true.
- **Orphan pruning.** A directory removed out-of-band leaves a row that is
  pruned lazily on next access and by reconcile on rebuild.
- **Schema-version bump.** Changing the code index version drops and rebuilds
  the table from the filesystem.
- **Cursor compatibility.** A cursor issued by the previous implementation pages
  correctly against the SQL-backed implementation.

## Rollout

The change is store-internal and backward compatible. On first startup after
upgrade, `index.db` is absent, so a background rebuild populates it from existing
sandbox directories while the daemon serves requests. No external contract, data
migration, or client change is required, and `index.db` can be deleted to force a
clean rebuild if needed.
