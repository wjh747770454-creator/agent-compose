//go:build boxlitecgo

package driver

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/runtimecache"

	_ "modernc.org/sqlite"
)

func TestCleanupExpiredCacheDirsKeepsCurrentAndFreshEntries(t *testing.T) {
	now := time.Now().UTC()
	root := filepath.Join(t.TempDir(), "image-cache")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir image-cache: %v", err)
	}

	staleDir := filepath.Join(root, "stale")
	currentDir := filepath.Join(root, "current")
	freshDir := filepath.Join(root, "fresh")
	for _, dir := range []string{staleDir, currentDir, freshDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	staleAt := now.Add(-2 * time.Hour)
	if err := os.Chtimes(staleDir, staleAt, staleAt); err != nil {
		t.Fatalf("chtimes stale dir: %v", err)
	}
	if err := os.Chtimes(currentDir, staleAt, staleAt); err != nil {
		t.Fatalf("chtimes current dir: %v", err)
	}

	removed, err := cleanupExpiredCacheDirs(root, time.Hour, map[string]struct{}{"current": {}}, now)
	if err != nil {
		t.Fatalf("cleanupExpiredCacheDirs: %v", err)
	}
	if len(removed) != 1 || removed[0] != staleDir {
		t.Fatalf("removed = %#v, want [%q]", removed, staleDir)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("stale dir exists after cleanup, err=%v", err)
	}
	if _, err := os.Stat(currentDir); err != nil {
		t.Fatalf("current dir missing after cleanup: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("fresh dir missing after cleanup: %v", err)
	}
}

func TestCleanupExpiredCacheDirsPrunesStaleArtifactsInsideCurrentEntry(t *testing.T) {
	now := time.Now().UTC()
	root := filepath.Join(t.TempDir(), "image-cache")
	currentDir := filepath.Join(root, "current")
	rootfsDir := filepath.Join(currentDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		t.Fatalf("mkdir rootfs dir: %v", err)
	}
	readyFlag := filepath.Join(currentDir, ".rootfs.ready")
	if err := os.WriteFile(readyFlag, []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write rootfs ready flag: %v", err)
	}
	staleAt := now.Add(-2 * time.Hour)
	if err := os.Chtimes(rootfsDir, staleAt, staleAt); err != nil {
		t.Fatalf("chtimes rootfs dir: %v", err)
	}
	if err := os.Chtimes(readyFlag, staleAt, staleAt); err != nil {
		t.Fatalf("chtimes ready flag: %v", err)
	}

	removed, err := cleanupExpiredCacheDirs(root, time.Hour, map[string]struct{}{"current": {}}, now)
	if err != nil {
		t.Fatalf("cleanupExpiredCacheDirs: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed count = %d, want 2 (%#v)", len(removed), removed)
	}
	if _, err := os.Stat(currentDir); err != nil {
		t.Fatalf("current dir missing after artifact cleanup: %v", err)
	}
	if _, err := os.Stat(rootfsDir); !os.IsNotExist(err) {
		t.Fatalf("rootfs dir exists after cleanup, err=%v", err)
	}
	if _, err := os.Stat(readyFlag); !os.IsNotExist(err) {
		t.Fatalf("ready flag exists after cleanup, err=%v", err)
	}
}

func TestBoxliteStartupPathsDoNotCallCacheCleanupHelpers(t *testing.T) {
	ensureCalls := boxliteMethodCalls(t, "EnsureSession")
	if containsString(ensureCalls, "cleanupLegacyBoxliteCaches") {
		t.Fatalf("EnsureSession calls cleanupLegacyBoxliteCaches: %#v", ensureCalls)
	}
	resolveCalls := boxliteMethodCalls(t, "resolveRootfsPath")
	if containsString(resolveCalls, "maybeRunCacheGC") {
		t.Fatalf("resolveRootfsPath calls maybeRunCacheGC: %#v", resolveCalls)
	}
}

func TestResolveRootfsPathDoesNotPruneMaterializedCache(t *testing.T) {
	dataRoot := t.TempDir()
	cacheDir := filepath.Join(dataRoot, "image-cache", "stale-image")
	for _, path := range []string{
		filepath.Join(cacheDir, "rootfs", "bin"),
		filepath.Join(cacheDir, "oci.tmp", "index.json"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir parent for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	for _, path := range []string{cacheDir, filepath.Join(cacheDir, "rootfs"), filepath.Join(cacheDir, "oci.tmp")} {
		if err := os.Chtimes(path, staleAt, staleAt); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}
	runtime := &cgoBoxRuntime{config: &appconfig.Config{
		DataRoot:      dataRoot,
		BoxRootfsPath: "/prebuilt/rootfs",
		BoxCacheTTL:   time.Nanosecond,
	}}

	rootfsPath, err := runtime.resolveRootfsPath(context.Background(), "guest:latest")
	if err != nil {
		t.Fatalf("resolveRootfsPath: %v", err)
	}
	if rootfsPath != "/prebuilt/rootfs" {
		t.Fatalf("rootfsPath = %q, want /prebuilt/rootfs", rootfsPath)
	}
	for _, path := range []string{cacheDir, filepath.Join(cacheDir, "rootfs"), filepath.Join(cacheDir, "oci.tmp")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("materialized cache path %s missing after resolveRootfsPath: %v", path, err)
		}
	}
}

func TestHasActiveBoxliteBoxes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "boxlite.db")
	db := createBoxliteStateDB(t, dbPath)
	insertBoxliteState(t, db, "stopped-box", "stopped")
	active, err := hasActiveBoxliteBoxes(dbPath)
	if err != nil {
		t.Fatalf("hasActiveBoxliteBoxes(stopped): %v", err)
	}
	if active {
		t.Fatalf("active = true, want false")
	}
	insertBoxliteState(t, db, "running-box", "running")
	active, err = hasActiveBoxliteBoxes(dbPath)
	if err != nil {
		t.Fatalf("hasActiveBoxliteBoxes(running): %v", err)
	}
	if !active {
		t.Fatalf("active = false, want true")
	}
}

func TestListBoxliteRuntimeDerivedCachesScansLegacyRoots(t *testing.T) {
	boxliteHome := t.TempDir()
	db := createBoxliteStateDB(t, filepath.Join(boxliteHome, boxliteDBFileName))
	insertBoxliteState(t, db, "stopped-box", "stopped")
	localPath := writeBoxliteCacheFile(t, boxliteHome, "images", "local", "legacy-a", "layer.txt")
	diskPath := writeBoxliteCacheFile(t, boxliteHome, "images", "disk-images", "legacy-b.ext4")

	result, err := listBoxliteRuntimeDerivedCaches(context.Background(), boxliteHome)
	if err != nil {
		t.Fatalf("listBoxliteRuntimeDerivedCaches: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	if len(result.Items) != 2 {
		t.Fatalf("item count = %d, want 2 (%#v)", len(result.Items), result.Items)
	}
	items := map[string]runtimecache.Item{}
	for _, item := range result.Items {
		items[item.Kind] = item
		if item.Domain != runtimecache.DomainRuntimeDerivedCache {
			t.Fatalf("domain = %q, want %q", item.Domain, runtimecache.DomainRuntimeDerivedCache)
		}
		if item.Driver != runtimecache.DriverBoxLite {
			t.Fatalf("driver = %q, want %q", item.Driver, runtimecache.DriverBoxLite)
		}
		if item.Status != runtimecache.StatusOrphaned || !item.Removable {
			t.Fatalf("item status/removable = %s/%v, want orphaned/true (%#v)", item.Status, item.Removable, item)
		}
		if item.CacheID == "" {
			t.Fatalf("cache id is empty for item %#v", item)
		}
	}
	if got := items[boxliteLocalImageCacheKind].Path; got != filepath.Dir(localPath) {
		t.Fatalf("local item path = %q, want %q", got, filepath.Dir(localPath))
	}
	if got := items[boxliteDiskImageCacheKind].Path; got != diskPath {
		t.Fatalf("disk item path = %q, want %q", got, diskPath)
	}
}

func TestBoxliteRuntimeDerivedPruneActiveBlocksDeletion(t *testing.T) {
	boxliteHome := t.TempDir()
	db := createBoxliteStateDB(t, filepath.Join(boxliteHome, boxliteDBFileName))
	insertBoxliteState(t, db, "running-box", "running")
	target := filepath.Dir(writeBoxliteCacheFile(t, boxliteHome, "images", "local", "legacy-a", "layer.txt"))

	result, err := pruneBoxliteRuntimeDerivedCaches(context.Background(), boxliteHome, runtimecache.PruneRequest{
		Filter: runtimecache.Filter{
			Driver: runtimecache.DriverBoxLite,
			Domain: runtimecache.DomainRuntimeDerivedCache,
		},
		Force: true,
	})
	if err != nil {
		t.Fatalf("pruneBoxliteRuntimeDerivedCaches: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("removed = %#v, want none", result.Removed)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("skipped count = %d, want 1 (%#v)", len(result.Skipped), result.Skipped)
	}
	if item := result.Skipped[0]; item.Status != runtimecache.StatusActive || item.Removable {
		t.Fatalf("skipped item status/removable = %s/%v, want active/false (%#v)", item.Status, item.Removable, item)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target missing after active prune: %v", err)
	}
}

func TestBoxliteRuntimeDerivedRemoveDryRunAndForceDeletesOnlyTarget(t *testing.T) {
	boxliteHome := t.TempDir()
	db := createBoxliteStateDB(t, filepath.Join(boxliteHome, boxliteDBFileName))
	insertBoxliteState(t, db, "stopped-box", "stopped")
	target := filepath.Dir(writeBoxliteCacheFile(t, boxliteHome, "images", "local", "legacy-a", "layer.txt"))
	sibling := filepath.Dir(writeBoxliteCacheFile(t, boxliteHome, "images", "local", "legacy-b", "layer.txt"))
	disk := writeBoxliteCacheFile(t, boxliteHome, "images", "disk-images", "legacy-c.ext4")
	list, err := listBoxliteRuntimeDerivedCaches(context.Background(), boxliteHome)
	if err != nil {
		t.Fatalf("listBoxliteRuntimeDerivedCaches: %v", err)
	}
	var cacheID string
	for _, item := range list.Items {
		if item.Path == target {
			cacheID = item.CacheID
			break
		}
	}
	if cacheID == "" {
		t.Fatalf("target cache id not found in %#v", list.Items)
	}

	dryRun, err := removeBoxliteRuntimeDerivedCache(context.Background(), boxliteHome, runtimecache.RemoveRequest{CacheID: cacheID})
	if err != nil {
		t.Fatalf("removeBoxliteRuntimeDerivedCache dry-run: %v", err)
	}
	if !dryRun.DryRun || len(dryRun.Removed) != 0 {
		t.Fatalf("dry-run result = %#v, want dry run with no removals", dryRun)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target missing after dry-run: %v", err)
	}

	forced, err := removeBoxliteRuntimeDerivedCache(context.Background(), boxliteHome, runtimecache.RemoveRequest{CacheID: cacheID, Force: true})
	if err != nil {
		t.Fatalf("removeBoxliteRuntimeDerivedCache force: %v", err)
	}
	if forced.DryRun || len(forced.Removed) != 1 || forced.Removed[0] != cacheID {
		t.Fatalf("force result = %#v, want one removal for %s", forced, cacheID)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target exists after force remove, err=%v", err)
	}
	for _, path := range []string{sibling, disk} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("sibling path %s missing after targeted remove: %v", path, err)
		}
	}
}

func TestBoxliteRuntimeDerivedRemoveRejectsSymlinkEscape(t *testing.T) {
	boxliteHome := t.TempDir()
	db := createBoxliteStateDB(t, filepath.Join(boxliteHome, boxliteDBFileName))
	insertBoxliteState(t, db, "stopped-box", "stopped")
	outside := filepath.Join(t.TempDir(), "outside-cache")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside target: %v", err)
	}
	root := filepath.Join(boxliteHome, "images", "local")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir local root: %v", err)
	}
	linkPath := filepath.Join(root, "escape")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatalf("symlink escape target: %v", err)
	}
	list, err := listBoxliteRuntimeDerivedCaches(context.Background(), boxliteHome)
	if err != nil {
		t.Fatalf("listBoxliteRuntimeDerivedCaches: %v", err)
	}
	var cacheID string
	for _, item := range list.Items {
		if item.Path == linkPath {
			cacheID = item.CacheID
			break
		}
	}
	if cacheID == "" {
		t.Fatalf("symlink cache id not found in %#v", list.Items)
	}

	result, err := removeBoxliteRuntimeDerivedCache(context.Background(), boxliteHome, runtimecache.RemoveRequest{CacheID: cacheID, Force: true})
	if err != nil {
		t.Fatalf("removeBoxliteRuntimeDerivedCache symlink escape: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("removed = %#v, want none", result.Removed)
	}
	if len(result.Skipped) != 1 || len(result.Warnings) == 0 {
		t.Fatalf("result = %#v, want skipped item with warning", result)
	}
	if _, err := os.Lstat(linkPath); err != nil {
		t.Fatalf("symlink was removed: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside target was removed: %v", err)
	}
}

func TestBoxliteRuntimeDerivedUnknownDBAndSchemaAreNotRemovable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, boxliteHome string)
	}{
		{
			name:  "missing-db",
			setup: func(t *testing.T, boxliteHome string) {},
		},
		{
			name: "missing-table",
			setup: func(t *testing.T, boxliteHome string) {
				dbPath := filepath.Join(boxliteHome, boxliteDBFileName)
				db, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatalf("open sqlite db: %v", err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if _, err := db.Exec(`CREATE TABLE other_state (id TEXT PRIMARY KEY NOT NULL);`); err != nil {
					t.Fatalf("create unrelated table: %v", err)
				}
			},
		},
		{
			name: "missing-status-column",
			setup: func(t *testing.T, boxliteHome string) {
				dbPath := filepath.Join(boxliteHome, boxliteDBFileName)
				db, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatalf("open sqlite db: %v", err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if _, err := db.Exec(`CREATE TABLE box_state (id TEXT PRIMARY KEY NOT NULL);`); err != nil {
					t.Fatalf("create box_state without status: %v", err)
				}
			},
		},
		{
			name: "corrupt-db",
			setup: func(t *testing.T, boxliteHome string) {
				if err := os.WriteFile(filepath.Join(boxliteHome, boxliteDBFileName), []byte("not sqlite"), 0o644); err != nil {
					t.Fatalf("write corrupt db: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			boxliteHome := t.TempDir()
			tc.setup(t, boxliteHome)
			target := filepath.Dir(writeBoxliteCacheFile(t, boxliteHome, "images", "local", "legacy-a", "layer.txt"))

			result, err := pruneBoxliteRuntimeDerivedCaches(context.Background(), boxliteHome, runtimecache.PruneRequest{
				Filter: runtimecache.Filter{
					Driver: runtimecache.DriverBoxLite,
					Domain: runtimecache.DomainRuntimeDerivedCache,
				},
				Force: true,
			})
			if err != nil {
				t.Fatalf("pruneBoxliteRuntimeDerivedCaches: %v", err)
			}
			if len(result.Removed) != 0 {
				t.Fatalf("removed = %#v, want none", result.Removed)
			}
			if len(result.Skipped) != 1 {
				t.Fatalf("skipped count = %d, want 1 (%#v)", len(result.Skipped), result.Skipped)
			}
			if item := result.Skipped[0]; item.Status != runtimecache.StatusUnknown || item.Removable {
				t.Fatalf("skipped item status/removable = %s/%v, want unknown/false (%#v)", item.Status, item.Removable, item)
			}
			if len(result.Warnings) == 0 {
				t.Fatalf("warnings empty, want unknown state warning")
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("target missing after unknown prune: %v", err)
			}
		})
	}
}

func TestCleanupLegacyBoxliteImageCachesUsesInventoryAwareRemoval(t *testing.T) {
	boxliteHome := t.TempDir()
	db := createBoxliteStateDB(t, filepath.Join(boxliteHome, boxliteDBFileName))
	insertBoxliteState(t, db, "stopped-box", "stopped")
	local := filepath.Dir(writeBoxliteCacheFile(t, boxliteHome, "images", "local", "legacy-a", "layer.txt"))
	disk := writeBoxliteCacheFile(t, boxliteHome, "images", "disk-images", "legacy-b.ext4")
	untouched := writeBoxliteCacheFile(t, boxliteHome, "other", "state.txt")

	removed, err := cleanupLegacyBoxliteImageCaches(boxliteHome)
	if err != nil {
		t.Fatalf("cleanupLegacyBoxliteImageCaches: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed count = %d, want 2 (%#v)", len(removed), removed)
	}
	for _, path := range []string{local, disk} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("path %s exists after cleanup, err=%v", path, err)
		}
	}
	if _, err := os.Stat(untouched); err != nil {
		t.Fatalf("non-cache path was removed: %v", err)
	}
}

func TestCleanupLegacyBoxliteImageCachesDoesNotUseRelativeHome(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	relativeHome := "relative-boxlite-home-for-test"
	target := filepath.Join(cwd, relativeHome, "images", "local", "legacy-a", "layer.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir relative target: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(cwd, relativeHome)) })
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write relative target: %v", err)
	}

	removed, err := cleanupLegacyBoxliteImageCaches(relativeHome)
	if err != nil {
		t.Fatalf("cleanupLegacyBoxliteImageCaches(relative): %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %#v, want none", removed)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("relative target missing after cleanup: %v", err)
	}
}

func createBoxliteStateDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir db parent: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE box_state (id TEXT PRIMARY KEY NOT NULL, status TEXT NOT NULL, pid INTEGER, json TEXT NOT NULL);`); err != nil {
		t.Fatalf("create box_state: %v", err)
	}
	return db
}

func insertBoxliteState(t *testing.T, db *sql.DB, id, status string) {
	t.Helper()
	pid := "NULL"
	if strings.TrimSpace(status) == "running" {
		pid = "123"
	}
	if _, err := db.Exec(`INSERT INTO box_state(id, status, pid, json) VALUES(?, ?, `+pid+`, '{}');`, id, status); err != nil {
		t.Fatalf("insert boxlite state %s/%s: %v", id, status, err)
	}
}

func writeBoxliteCacheFile(t *testing.T, boxliteHome string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{boxliteHome}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func boxliteMethodCalls(t *testing.T, methodName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "boxlite_cgo.go", nil, 0)
	if err != nil {
		t.Fatalf("parse boxlite_cgo.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != methodName {
			continue
		}
		var calls []string
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				calls = append(calls, fun.Name)
			case *ast.SelectorExpr:
				calls = append(calls, fun.Sel.Name)
			}
			return true
		})
		return calls
	}
	t.Fatalf("method %s not found in boxlite_cgo.go", methodName)
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
