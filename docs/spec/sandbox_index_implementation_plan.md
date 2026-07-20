# Sandbox Summary Index Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Store.ListSandboxes` answer from an indexed SQLite cache instead of scanning `SandboxRoot`, so list cost is bounded by page size rather than total sandbox count.

**Architecture:** A dedicated SQLite database at `SandboxRoot/index.db`, owned by `sessionstore.Store`, mirrors a queryable summary of every sandbox. The filesystem stays authoritative; the index is a rebuildable cache maintained write-through on every mutation and rebuilt in the background when absent or when its schema version changes. `ListSandboxes` keeps its exact existing `SandboxListResult` contract (offset **and** keyset callers both continue to work); only its data source changes.

**Tech Stack:** Go, `database/sql`, `modernc.org/sqlite` (pure-Go driver, already used by `configstore`), `samber/do` DI.

## Global Constraints

- Driver: `modernc.org/sqlite`, opened as `sql.Open("sqlite", path)`. Single writer: `db.SetMaxOpenConns(1)`.
- The filesystem (`SandboxRoot/<id>/metadata.json`) is the source of truth. Index write failures are logged at `warn` and never fail the sandbox mutation.
- No external contract changes: the `ListSandboxes` gRPC request/response, the opaque cursor format, and `GetSandbox` behavior stay identical.
- `SandboxListResult` must keep populating `TotalCount`, `HasMore`, and `NextOffset` with the same semantics as today, because `capability_sandbox.go`, `volumes/manager.go`, and `session_rpc_bridge.go` depend on offset pagination.
- Timestamps are stored as `INTEGER` Unix milliseconds (`time.Time.UnixMilli()`).
- Commit messages: conventional-commit style, no `Co-Authored-By` / no Claude attribution (repo convention).
- Reference: `docs/design/sandbox_index_design.md`.

---

### Task 1: Index database skeleton — open, schema, version-gated rebuild detection

**Files:**
- Create: `pkg/storage/sessionstore/sandbox_index.go`
- Create: `pkg/storage/sessionstore/sandbox_index_test.go`
- Modify: `pkg/model/model.go` (add `IndexRebuilding bool` to `SandboxListResult`)

**Interfaces:**
- Produces:
  - `const sandboxIndexVersion = 1`
  - `type sandboxIndex struct { db *sql.DB }`
  - `func openSandboxIndex(path string) (*sandboxIndex, needsRebuild bool, err error)` — opens/creates `index.db`; returns `needsRebuild=true` when `PRAGMA user_version` differs from `sandboxIndexVersion` (drops and recreates the table, sets the version).
  - `func (x *sandboxIndex) Close() error`

- [ ] **Step 1: Write the failing test**

```go
// pkg/storage/sessionstore/sandbox_index_test.go
package sessionstore

import (
	"path/filepath"
	"testing"
)

func TestOpenSandboxIndexCreatesSchemaAndDetectsVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")

	// First open on a fresh file: schema created, rebuild needed (was version 0).
	idx, needsRebuild, err := openSandboxIndex(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !needsRebuild {
		t.Fatalf("fresh index should need rebuild")
	}
	if _, err := idx.db.Exec(`INSERT INTO sandboxes(id, updated_at) VALUES('a', 1)`); err != nil {
		t.Fatalf("schema missing: %v", err)
	}
	_ = idx.Close()

	// Reopen at the same version: no rebuild needed, data preserved.
	idx2, needsRebuild2, err := openSandboxIndex(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if needsRebuild2 {
		t.Fatalf("same-version reopen should not need rebuild")
	}
	var count int
	if err := idx2.db.QueryRow(`SELECT COUNT(*) FROM sandboxes`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected preserved row, got %d", count)
	}
	_ = idx2.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/storage/sessionstore/ -run TestOpenSandboxIndexCreatesSchemaAndDetectsVersion -v`
Expected: FAIL — `undefined: openSandboxIndex`.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/storage/sessionstore/sandbox_index.go
package sessionstore

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const sandboxIndexVersion = 1

type sandboxIndex struct {
	db *sql.DB
}

func openSandboxIndex(path string) (*sandboxIndex, bool, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, false, fmt.Errorf("open sandbox index: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		_ = db.Close()
		return nil, false, fmt.Errorf("read sandbox index version: %w", err)
	}

	needsRebuild := version != sandboxIndexVersion
	if needsRebuild {
		if _, err := db.Exec(`DROP TABLE IF EXISTS sandboxes`); err != nil {
			_ = db.Close()
			return nil, false, fmt.Errorf("drop stale sandbox index: %w", err)
		}
	}
	if _, err := db.Exec(sandboxIndexSchema); err != nil {
		_ = db.Close()
		return nil, false, fmt.Errorf("create sandbox index schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, sandboxIndexVersion)); err != nil {
		_ = db.Close()
		return nil, false, fmt.Errorf("set sandbox index version: %w", err)
	}
	return &sandboxIndex{db: db}, needsRebuild, nil
}

func (x *sandboxIndex) Close() error {
	if x == nil || x.db == nil {
		return nil
	}
	return x.db.Close()
}

const sandboxIndexSchema = `
CREATE TABLE IF NOT EXISTS sandboxes (
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
CREATE INDEX IF NOT EXISTS idx_sandboxes_updated ON sandboxes(updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_sandboxes_vm_status_updated ON sandboxes(vm_status, updated_at DESC, id DESC);
`
```

Also add the result field:

```go
// pkg/model/model.go — in type SandboxListResult struct, add:
	IndexRebuilding bool
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/storage/sessionstore/ -run TestOpenSandboxIndexCreatesSchemaAndDetectsVersion -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/sessionstore/sandbox_index.go pkg/storage/sessionstore/sandbox_index_test.go pkg/model/model.go
git commit -m "feat(sessionstore): add sandbox index db skeleton with version-gated schema"
```

---

### Task 2: Row projection, Upsert, Delete

**Files:**
- Modify: `pkg/storage/sessionstore/sandbox_index.go`
- Modify: `pkg/storage/sessionstore/sandbox_index_test.go`

**Interfaces:**
- Consumes: `sandboxIndex` (Task 1), `domain.Sandbox` / `domain.SandboxSummary` (`Summary.ID/ShortID/Title/TriggerSource/Driver/VMStatus/WorkspacePath/CreatedAt/UpdatedAt`, plus `WorkspaceID` and optional `Workspace.{ID,Name,Type}`).
- Produces:
  - `func (x *sandboxIndex) Upsert(ctx context.Context, sb *domain.Sandbox) error` — last-writer-wins on `updated_at`.
  - `func (x *sandboxIndex) Delete(ctx context.Context, id string) error`

- [ ] **Step 1: Write the failing test**

```go
// append to pkg/storage/sessionstore/sandbox_index_test.go
import (
	"context"
	"time"

	domain "agent-compose/pkg/model"
)

func newTestIndex(t *testing.T) *sandboxIndex {
	t.Helper()
	idx, _, err := openSandboxIndex(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func sb(id string, updated time.Time) *domain.Sandbox {
	return &domain.Sandbox{Summary: domain.SandboxSummary{
		ID: id, Driver: "docker", VMStatus: "RUNNING",
		TriggerSource: "manual", Title: "t-" + id,
		CreatedAt: updated, UpdatedAt: updated,
	}}
}

func TestSandboxIndexUpsertDeleteAndStaleGuard(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	t0 := time.Unix(1000, 0).UTC()
	t1 := time.Unix(2000, 0).UTC()

	if err := idx.Upsert(ctx, sb("x", t1)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Stale upsert (older updated_at) must not overwrite.
	stale := sb("x", t0)
	stale.Summary.Driver = "STALE"
	if err := idx.Upsert(ctx, stale); err != nil {
		t.Fatalf("stale upsert: %v", err)
	}
	var driver string
	var updated int64
	if err := idx.db.QueryRow(`SELECT driver, updated_at FROM sandboxes WHERE id='x'`).Scan(&driver, &updated); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if driver != "docker" || updated != t1.UnixMilli() {
		t.Fatalf("stale write leaked: driver=%s updated=%d", driver, updated)
	}

	if err := idx.Delete(ctx, "x"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var count int
	_ = idx.db.QueryRow(`SELECT COUNT(*) FROM sandboxes`).Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", count)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/storage/sessionstore/ -run TestSandboxIndexUpsertDeleteAndStaleGuard -v`
Expected: FAIL — `idx.Upsert undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
// append to pkg/storage/sessionstore/sandbox_index.go
import (
	"context"
	// keep existing imports; add:
	domain "agent-compose/pkg/model"
)

func (x *sandboxIndex) Upsert(ctx context.Context, sb *domain.Sandbox) error {
	if sb == nil || sb.Summary.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	s := sb.Summary
	var wsID, wsName, wsType string
	wsID = sb.WorkspaceID
	if sb.Workspace != nil {
		if wsID == "" {
			wsID = sb.Workspace.ID
		}
		wsName = sb.Workspace.Name
		wsType = sb.Workspace.Type
	}
	_, err := x.db.ExecContext(ctx, `
INSERT INTO sandboxes (id, short_id, title, trigger_source, driver, vm_status,
	workspace_path, workspace_id, workspace_name, workspace_type, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	short_id=excluded.short_id, title=excluded.title, trigger_source=excluded.trigger_source,
	driver=excluded.driver, vm_status=excluded.vm_status, workspace_path=excluded.workspace_path,
	workspace_id=excluded.workspace_id, workspace_name=excluded.workspace_name,
	workspace_type=excluded.workspace_type, created_at=excluded.created_at, updated_at=excluded.updated_at
WHERE excluded.updated_at >= sandboxes.updated_at`,
		s.ID, s.ShortID, s.Title, s.TriggerSource, s.Driver, s.VMStatus,
		s.WorkspacePath, wsID, wsName, wsType, s.CreatedAt.UnixMilli(), s.UpdatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("upsert sandbox index %s: %w", s.ID, err)
	}
	return nil
}

func (x *sandboxIndex) Delete(ctx context.Context, id string) error {
	if _, err := x.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete sandbox index %s: %w", id, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/storage/sessionstore/ -run TestSandboxIndexUpsertDeleteAndStaleGuard -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/sessionstore/sandbox_index.go pkg/storage/sessionstore/sandbox_index_test.go
git commit -m "feat(sessionstore): upsert and delete sandbox index rows with stale-write guard"
```

---

### Task 3: Query builder + list query (filters, keyset, offset, count, ordering)

**Files:**
- Create: `pkg/storage/sessionstore/sandbox_index_list.go`
- Modify: `pkg/storage/sessionstore/sandbox_index_test.go`

**Interfaces:**
- Consumes: `sandboxIndex` (Task 1), `domain.SandboxListOptions`, `domain.SandboxTypeFromTriggerSource`, `domain.SandboxTypeScript`, `domain.SandboxTypeManual`.
- Produces:
  - `func sandboxWhere(o domain.SandboxListOptions) (string, []any)` — builds the `WHERE` clause (empty string when no predicates) covering every `SandboxListOptions` filter field including keyset.
  - `func (x *sandboxIndex) list(ctx context.Context, o domain.SandboxListOptions, sandboxDir func(id string) string) (page []*domain.Sandbox, total int, err error)` — returns the page (ordered `updated_at DESC, id DESC`, applying `Offset`/`Limit`) and the total matching count. `sandboxDir` is used to recompute each hydrated sandbox's `Summary.WorkspacePath`, matching the old `loadSandbox` behavior.

- [ ] **Step 1: Write the failing test**

```go
// append to pkg/storage/sessionstore/sandbox_index_test.go
func TestSandboxIndexListFiltersOrderKeysetOffset(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	dir := func(id string) string { return "/root/" + id }

	base := time.Unix(10_000, 0).UTC()
	// Three sandboxes at ascending updated_at.
	a := sb("a", base.Add(1*time.Second))
	b := sb("b", base.Add(2*time.Second))
	c := sb("c", base.Add(3*time.Second))
	b.Summary.Driver = "boxlite"
	for _, s := range []*domain.Sandbox{a, b, c} {
		if err := idx.Upsert(ctx, s); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// Default order is updated_at DESC → c, b, a.
	page, total, err := idx.list(ctx, domain.SandboxListOptions{Limit: 10}, dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(page) != 3 || page[0].Summary.ID != "c" || page[2].Summary.ID != "a" {
		t.Fatalf("order/total wrong: total=%d ids=%v", total, ids(page))
	}
	if page[0].Summary.WorkspacePath != "/root/c/workspace" {
		t.Fatalf("workspace path not recomputed: %s", page[0].Summary.WorkspacePath)
	}

	// Driver filter.
	page, total, _ = idx.list(ctx, domain.SandboxListOptions{Driver: "boxlite", Limit: 10}, dir)
	if total != 1 || len(page) != 1 || page[0].Summary.ID != "b" {
		t.Fatalf("driver filter wrong: %v", ids(page))
	}

	// Keyset: everything strictly older than c → b, a.
	page, _, _ = idx.list(ctx, domain.SandboxListOptions{
		BeforeUpdatedAt: c.Summary.UpdatedAt, BeforeID: "c", Limit: 10,
	}, dir)
	if len(page) != 2 || page[0].Summary.ID != "b" {
		t.Fatalf("keyset wrong: %v", ids(page))
	}

	// Offset pagination: page size 2 → [c,b] then [a].
	page, total, _ = idx.list(ctx, domain.SandboxListOptions{Offset: 0, Limit: 2}, dir)
	if total != 3 || len(page) != 2 {
		t.Fatalf("offset page1 wrong: total=%d n=%d", total, len(page))
	}
	page, _, _ = idx.list(ctx, domain.SandboxListOptions{Offset: 2, Limit: 2}, dir)
	if len(page) != 1 || page[0].Summary.ID != "a" {
		t.Fatalf("offset page2 wrong: %v", ids(page))
	}
}

func ids(page []*domain.Sandbox) []string {
	out := make([]string, 0, len(page))
	for _, s := range page {
		out = append(out, s.Summary.ID)
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/storage/sessionstore/ -run TestSandboxIndexListFiltersOrderKeysetOffset -v`
Expected: FAIL — `idx.list undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/storage/sessionstore/sandbox_index_list.go
package sessionstore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	domain "agent-compose/pkg/model"
)

func sandboxWhere(o domain.SandboxListOptions) (string, []any) {
	var conds []string
	var args []any
	like := func(col, v string) {
		conds = append(conds, "LOWER("+col+") LIKE ?")
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(v))+"%")
	}
	if v := strings.TrimSpace(o.SandboxType); v != "" {
		if strings.EqualFold(v, domain.SandboxTypeScript) {
			conds = append(conds, "trigger_source LIKE ?")
			args = append(args, domain.SandboxTypeScript+":%")
		} else { // manual: NOT a script:* trigger source
			conds = append(conds, "trigger_source NOT LIKE ?")
			args = append(args, domain.SandboxTypeScript+":%")
		}
	}
	if v := strings.TrimSpace(o.TriggerSourceQuery); v != "" {
		like("trigger_source", v)
	}
	if v := strings.TrimSpace(o.TitleQuery); v != "" {
		like("title", v)
	}
	if v := strings.TrimSpace(o.WorkspaceQuery); v != "" {
		p := "%" + strings.ToLower(strings.TrimSpace(v)) + "%"
		conds = append(conds, "(LOWER(workspace_path) LIKE ? OR LOWER(workspace_id) LIKE ? OR LOWER(workspace_name) LIKE ? OR LOWER(workspace_type) LIKE ?)")
		args = append(args, p, p, p, p)
	}
	if v := strings.TrimSpace(o.Driver); v != "" {
		conds = append(conds, "LOWER(driver) = ?")
		args = append(args, strings.ToLower(v))
	}
	if v := strings.TrimSpace(o.VMStatus); v != "" {
		conds = append(conds, "UPPER(vm_status) = ?")
		args = append(args, strings.ToUpper(v))
	}
	if !o.CreatedFrom.IsZero() {
		conds = append(conds, "created_at >= ?")
		args = append(args, o.CreatedFrom.UnixMilli())
	}
	if !o.CreatedTo.IsZero() {
		conds = append(conds, "created_at <= ?")
		args = append(args, o.CreatedTo.UnixMilli())
	}
	if !o.UpdatedFrom.IsZero() {
		conds = append(conds, "updated_at >= ?")
		args = append(args, o.UpdatedFrom.UnixMilli())
	}
	if !o.UpdatedTo.IsZero() {
		conds = append(conds, "updated_at <= ?")
		args = append(args, o.UpdatedTo.UnixMilli())
	}
	if !o.BeforeUpdatedAt.IsZero() && o.BeforeID != "" {
		conds = append(conds, "(updated_at < ? OR (updated_at = ? AND id < ?))")
		ms := o.BeforeUpdatedAt.UnixMilli()
		args = append(args, ms, ms, o.BeforeID)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

const sandboxSelectCols = `id, short_id, title, trigger_source, driver, vm_status,
	workspace_path, workspace_id, workspace_name, workspace_type, created_at, updated_at`

func (x *sandboxIndex) list(ctx context.Context, o domain.SandboxListOptions, sandboxDir func(string) string) ([]*domain.Sandbox, int, error) {
	where, args := sandboxWhere(o)

	var total int
	if err := x.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sandboxes`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count sandbox index: %w", err)
	}

	offset, limit := domain.NormalizeSandboxListBounds(o.Offset, o.Limit)
	query := `SELECT ` + sandboxSelectCols + ` FROM sandboxes` + where +
		` ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := x.db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query sandbox index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var page []*domain.Sandbox
	for rows.Next() {
		sb, err := scanSandboxIndexRow(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		sb.Summary.WorkspacePath = filepath.Join(sandboxDir(sb.Summary.ID), "workspace")
		page = append(page, sb)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate sandbox index: %w", err)
	}
	return page, total, nil
}

func scanSandboxIndexRow(scan func(...any) error) (*domain.Sandbox, error) {
	var s domain.SandboxSummary
	var wsID, wsName, wsType string
	var created, updated int64
	if err := scan(&s.ID, &s.ShortID, &s.Title, &s.TriggerSource, &s.Driver, &s.VMStatus,
		&s.WorkspacePath, &wsID, &wsName, &wsType, &created, &updated); err != nil {
		return nil, fmt.Errorf("scan sandbox index row: %w", err)
	}
	s.CreatedAt = domain.UnixMilliToTime(created)
	s.UpdatedAt = domain.UnixMilliToTime(updated)
	return &domain.Sandbox{Summary: s, WorkspaceID: wsID,
		Workspace: &domain.SandboxWorkspace{ID: wsID, Name: wsName, Type: wsType}}, nil
}
```

Add the timestamp helper if it does not already exist (`pkg/model/loader_model.go` already has `NonZeroTimeUnixMilli`; add its inverse next to it):

```go
// pkg/model/loader_model.go
func UnixMilliToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/storage/sessionstore/ -run TestSandboxIndexListFiltersOrderKeysetOffset -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/sessionstore/sandbox_index_list.go pkg/storage/sessionstore/sandbox_index_test.go pkg/model/loader_model.go
git commit -m "feat(sessionstore): build indexed sandbox list query with filters, keyset, and offset"
```

---

### Task 4: Wire index into Store + background rebuild with reconcile

**Files:**
- Modify: `pkg/storage/sessionstore/store.go` (struct fields, `NewWithConfig`, add `rebuildIndex`)
- Modify: `pkg/storage/sessionstore/sandbox_index_test.go` (or a new `sandbox_index_store_test.go`)

**Interfaces:**
- Consumes: `sandboxIndex` + `openSandboxIndex` (Task 1), `Upsert`/`Delete` (Task 2), existing `Store.loadSandbox`, `Store.sandboxDir`, `s.config.SandboxRoot`.
- Produces:
  - New `Store` fields: `index *sandboxIndex`, `rebuilding atomic.Bool`.
  - `func (s *Store) rebuildIndex(ctx context.Context)` — walks `SandboxRoot`, upserts each sandbox, deletes index rows whose directory no longer exists, then clears `rebuilding`.

- [ ] **Step 1: Write the failing test**

```go
// pkg/storage/sessionstore/sandbox_index_store_test.go
package sessionstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	store, err := NewWithConfig(&appconfig.Config{SandboxRoot: root})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.index.Close() })
	return store
}

func TestRebuildIndexBackfillsAndPrunesOrphans(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Seed an orphan index row whose directory does not exist.
	if err := store.index.Upsert(ctx, sb("ghost", time.Unix(1, 0).UTC())); err != nil {
		t.Fatalf("seed ghost: %v", err)
	}
	// Create two real sandbox dirs with metadata.json via saveSandbox.
	for _, id := range []string{"real1", "real2"} {
		s := sb(id, time.Unix(100, 0).UTC())
		if err := os.MkdirAll(store.sandboxDir(id), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := store.saveSandbox(s); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	store.rebuildIndex(ctx)

	page, total, err := store.index.list(ctx, domain.SandboxListOptions{Limit: 10}, store.sandboxDir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 real rows after rebuild (ghost pruned), got %d: %v", total, ids(page))
	}
	if store.rebuilding.Load() {
		t.Fatalf("rebuilding flag should be cleared after rebuild")
	}
	_ = filepath.Join // keep import if unused elsewhere
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/storage/sessionstore/ -run TestRebuildIndexBackfillsAndPrunesOrphans -v`
Expected: FAIL — `store.index undefined` / `store.rebuildIndex undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/storage/sessionstore/store.go

// add import: "sync/atomic"

// extend the Store struct:
type Store struct {
	config                *appconfig.Config
	sandboxLocks          sync.Map
	cacheDependencyMu     sync.RWMutex
	cacheDependencyLocker CacheDependencyLocker
	index                 *sandboxIndex
	rebuilding            atomic.Bool
}
```

In `NewWithConfig`, after the store is constructed and `SandboxRoot` exists, open the index and kick off a rebuild when needed (do NOT block startup):

```go
	index, needsRebuild, err := openSandboxIndex(filepath.Join(config.SandboxRoot, "index.db"))
	if err != nil {
		return nil, fmt.Errorf("open sandbox index: %w", err)
	}
	store.index = index
	if needsRebuild {
		store.rebuilding.Store(true)
		go store.rebuildIndex(context.Background())
	}
	return store, nil
```

Add the rebuild method:

```go
func (s *Store) rebuildIndex(ctx context.Context) {
	defer s.rebuilding.Store(false)

	seen := map[string]struct{}{}
	entries, err := os.ReadDir(s.config.SandboxRoot)
	if err != nil {
		slog.Warn("sandbox index rebuild: read root failed", "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".lifecycle" {
			continue
		}
		sandbox, err := s.loadSandbox(entry.Name())
		if err != nil {
			continue
		}
		seen[sandbox.Summary.ID] = struct{}{}
		if err := s.index.Upsert(ctx, sandbox); err != nil {
			slog.Warn("sandbox index rebuild: upsert failed", "sandbox_id", sandbox.Summary.ID, "error", err)
		}
	}

	// Reconcile: prune rows whose directory is gone.
	rows, err := s.index.db.QueryContext(ctx, `SELECT id FROM sandboxes`)
	if err != nil {
		slog.Warn("sandbox index rebuild: reconcile query failed", "error", err)
		return
	}
	var orphans []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		if _, ok := seen[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	_ = rows.Close()
	for _, id := range orphans {
		if err := s.index.Delete(ctx, id); err != nil {
			slog.Warn("sandbox index rebuild: prune failed", "sandbox_id", id, "error", err)
		}
	}
}
```

Confirm `store.go` already imports `os`, `path/filepath`, and `log/slog` (it uses `os.ReadDir`, `filepath.Join`, and `slog` elsewhere); add any that are missing.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/storage/sessionstore/ -run TestRebuildIndexBackfillsAndPrunesOrphans -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/sessionstore/store.go pkg/storage/sessionstore/sandbox_index_store_test.go
git commit -m "feat(sessionstore): open sandbox index on startup and rebuild in background with reconcile"
```

---

### Task 5: Write-through hooks on mutations

**Files:**
- Modify: `pkg/storage/sessionstore/store.go` (`createSandboxWithOptions`, `UpdateSandbox`, `RemoveSandbox`, `AddEvent`)
- Modify: `pkg/storage/sessionstore/sandbox_index_store_test.go`

**Interfaces:**
- Consumes: `Store.index` (Task 4), `Upsert`/`Delete` (Task 2).
- Produces: no new exported symbols; adds a private helper `func (s *Store) recordIndex(ctx context.Context, sb *Sandbox)` used at each mutation.

- [ ] **Step 1: Write the failing test**

```go
// append to pkg/storage/sessionstore/sandbox_index_store_test.go
func TestWriteThroughKeepsIndexInSync(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s := sb("wt", time.Unix(100, 0).UTC())
	if err := os.MkdirAll(store.sandboxDir("wt"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := store.saveSandbox(s); err != nil {
		t.Fatalf("save: %v", err)
	}

	// UpdateSandbox must upsert the row.
	s.Summary.VMStatus = "STOPPED"
	if err := store.UpdateSandbox(ctx, s); err != nil {
		t.Fatalf("update: %v", err)
	}
	var status string
	if err := store.index.db.QueryRow(`SELECT vm_status FROM sandboxes WHERE id='wt'`).Scan(&status); err != nil {
		t.Fatalf("expected row after update: %v", err)
	}
	if status != "STOPPED" {
		t.Fatalf("index not updated: %s", status)
	}

	// RemoveSandbox must delete the row.
	if err := store.RemoveSandbox(ctx, "wt"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	var count int
	_ = store.index.db.QueryRow(`SELECT COUNT(*) FROM sandboxes WHERE id='wt'`).Scan(&count)
	if count != 0 {
		t.Fatalf("index row not deleted on remove")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/storage/sessionstore/ -run TestWriteThroughKeepsIndexInSync -v`
Expected: FAIL — index row missing after `UpdateSandbox` (no hook yet).

- [ ] **Step 3: Write minimal implementation**

Add the helper:

```go
func (s *Store) recordIndex(ctx context.Context, sb *Sandbox) {
	if s.index == nil || sb == nil {
		return
	}
	if err := s.index.Upsert(ctx, sb); err != nil {
		slog.Warn("sandbox index upsert failed", "sandbox_id", sb.Summary.ID, "error", err)
	}
}
```

Hook it in. In `createSandboxWithOptions`, immediately before `return session, nil`:

```go
	s.recordIndex(context.Background(), session)
	return session, nil
```

In `UpdateSandbox`, after `saveSandboxPreservingCounts` succeeds (before returning nil):

```go
	s.recordIndex(ctx, session)
	return nil
```

(Adjust to the method's actual return: record right after the successful save, keeping the existing error path unchanged.)

In `AddEvent`, after the summary count is persisted (where the sandbox summary is saved), add `s.recordIndex(ctx, session)` for the updated sandbox.

In `RemoveSandbox`, after `os.RemoveAll(path)` succeeds and before `s.sandboxLocks.Delete(...)`:

```go
	if s.index != nil {
		if err := s.index.Delete(ctx, id); err != nil {
			slog.Warn("sandbox index delete failed", "sandbox_id", id, "error", err)
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/storage/sessionstore/ -run TestWriteThroughKeepsIndexInSync -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/sessionstore/store.go pkg/storage/sessionstore/sandbox_index_store_test.go
git commit -m "feat(sessionstore): maintain sandbox index write-through on create, update, remove, event"
```

---

### Task 6: Point ListSandboxes at the index + lazy orphan prune

**Files:**
- Modify: `pkg/storage/sessionstore/store.go` (`ListSandboxes`, `loadSandbox`)
- Modify: `pkg/storage/sessionstore/sandbox_index_store_test.go`

**Interfaces:**
- Consumes: `sandboxIndex.list` (Task 3), `Store.index` + `rebuilding` (Task 4).
- Produces: `ListSandboxes` returns a `SandboxListResult` built from the index, preserving `TotalCount`/`HasMore`/`NextOffset` semantics and setting `IndexRebuilding`.

- [ ] **Step 1: Write the failing test**

```go
// append to pkg/storage/sessionstore/sandbox_index_store_test.go
func TestListSandboxesServesFromIndexWithContract(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i, id := range []string{"s1", "s2", "s3"} {
		s := sb(id, time.Unix(int64(100+i), 0).UTC())
		if err := os.MkdirAll(store.sandboxDir(id), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := store.saveSandbox(s); err != nil {
			t.Fatalf("save: %v", err)
		}
		store.recordIndex(ctx, s)
	}

	// Page 1 of 2: contract fields populated.
	res, err := store.ListSandboxes(ctx, domain.SandboxListOptions{Offset: 0, Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.TotalCount != 3 || !res.HasMore || res.NextOffset != 2 || len(res.Sandboxes) != 2 {
		t.Fatalf("contract wrong: total=%d hasMore=%v next=%d n=%d", res.TotalCount, res.HasMore, res.NextOffset, len(res.Sandboxes))
	}
	// Newest first.
	if res.Sandboxes[0].Summary.ID != "s3" {
		t.Fatalf("order wrong: %v", ids(res.Sandboxes))
	}

	res2, _ := store.ListSandboxes(ctx, domain.SandboxListOptions{Offset: 2, Limit: 2})
	if res2.HasMore || len(res2.Sandboxes) != 1 {
		t.Fatalf("page2 wrong: hasMore=%v n=%d", res2.HasMore, len(res2.Sandboxes))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/storage/sessionstore/ -run TestListSandboxesServesFromIndexWithContract -v`
Expected: FAIL — old `ListSandboxes` scans the filesystem and returns different results/paths (or the assertions on ordering/contract mismatch).

- [ ] **Step 3: Write minimal implementation**

Replace the body of `ListSandboxes`:

```go
func (s *Store) ListSandboxes(ctx context.Context, options SandboxListOptions) (SandboxListResult, error) {
	page, total, err := s.index.list(ctx, options, s.sandboxDir)
	if err != nil {
		return SandboxListResult{}, err
	}
	for _, sb := range page {
		s.hydrateSandboxGuestImage(sb)
	}
	offset, _ := domain.NormalizeSandboxListBounds(options.Offset, options.Limit)
	result := SandboxListResult{
		Sandboxes:       page,
		TotalCount:      total,
		HasMore:         offset+len(page) < total,
		NextOffset:      offset + len(page),
		IndexRebuilding: s.rebuilding.Load(),
	}
	if result.NextOffset > total {
		result.NextOffset = total
	}
	return result, nil
}
```

Add lazy pruning in `loadSandbox`: when `os.ReadFile(metadata.json)` returns a not-exist error, delete the stale index row before returning:

```go
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && s.index != nil {
			_ = s.index.Delete(context.Background(), strings.TrimSpace(id))
		}
		return nil, fmt.Errorf("read session metadata %s: %w", id, err)
	}
```

Confirm `errors` and `context` are imported in `store.go` (add if missing).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/storage/sessionstore/ -run TestListSandboxesServesFromIndexWithContract -v`
Expected: PASS.

- [ ] **Step 5: Run the full sessionstore + dependent suites**

Run:
```
go test ./pkg/storage/sessionstore/ ./pkg/agentcompose/api/ ./pkg/agentcompose/adapters/ ./pkg/sessions/ ./pkg/volumes/ ./pkg/dashboard/ ./pkg/projects/
```
Expected: PASS. If any pre-existing list test asserts full-scan behavior, update it to construct sandboxes through `saveSandbox` + `recordIndex` (or trigger a rebuild) so the index is populated.

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/sessionstore/store.go pkg/storage/sessionstore/sandbox_index_store_test.go
git commit -m "feat(sessionstore): serve ListSandboxes from the sqlite index with lazy orphan prune"
```

---

## Self-Review

**Spec coverage:**
- Index location/ownership (`index.db`, owned by Store) → Task 1, Task 4.
- Schema + indexes + `user_version` drop-and-rebuild → Task 1.
- Write-through Upsert/Delete + conflict clause + warn-only errors → Task 2, Task 5.
- Read path: SQL list + options→SQL mapping + `GetSandbox` unchanged + contract preserved → Task 3, Task 6.
- Background async rebuild + rebuilding visibility (`IndexRebuilding`) → Task 4, Task 6.
- Orphan pruning (normal remove / lazy / reconcile-on-rebuild) → Task 5 (remove), Task 6 (lazy), Task 4 (reconcile).
- External contract untouched (handler/cursor/proto) → Task 6 keeps `SandboxListResult` fields; no proto/handler edits.
- Non-goals (retention GC, moving metadata, ORM) → not implemented, by omission.

**Placeholder scan:** No TBD/TODO; every code step shows concrete code and every run step shows an expected result.

**Type consistency:** `sandboxIndex`, `openSandboxIndex`, `Upsert(ctx, *domain.Sandbox)`, `Delete(ctx, id)`, `list(ctx, options, sandboxDir)`, `scanSandboxIndexRow`, `sandboxWhere`, `recordIndex`, `rebuildIndex`, `rebuilding atomic.Bool`, `index *sandboxIndex`, and `SandboxListResult.IndexRebuilding` are used consistently across tasks. `UnixMilliToTime` is defined in Task 3 and paired with the existing `NonZeroTimeUnixMilli`.

**Known follow-ups (out of this plan's scope):** retention/disk GC; optionally skipping the `COUNT(*)` for pure keyset (offset==0) calls if profiling shows it matters at very large N.
```
