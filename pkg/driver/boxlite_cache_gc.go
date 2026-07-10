//go:build boxlitecgo

package driver

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"agent-compose/pkg/runtimecache"

	_ "modernc.org/sqlite"
)

const boxliteCacheGCMinInterval = 5 * time.Minute
const boxliteDBFileName = "boxlite.db"

const (
	boxliteLocalImageCacheKind = "boxlite-local-image"
	boxliteDiskImageCacheKind  = "boxlite-disk-image"
	boxliteLastUsedSourceMTime = "mtime"
	boxliteReferenceTypeDB     = "boxlite-db"
)

type boxliteCacheGCState struct {
	mu      sync.Mutex
	lastRun time.Time
}

func (r *cgoSandboxRuntime) maybeRunCacheGC(currentImageID string) {
	if r == nil || r.config == nil {
		return
	}
	now := time.Now().UTC()
	r.cache.mu.Lock()
	if !r.cache.lastRun.IsZero() && now.Sub(r.cache.lastRun) < boxliteCacheGCMinInterval {
		r.cache.mu.Unlock()
		return
	}
	r.cache.lastRun = now
	r.cache.mu.Unlock()

	if ttl := r.config.BoxCacheTTL; ttl > 0 {
		removed, err := cleanupExpiredCacheDirs(filepath.Join(r.config.DataRoot, "image-cache"), ttl, map[string]struct{}{strings.TrimSpace(currentImageID): {}}, now)
		if err != nil {
			slog.Warn("agent-compose boxlite cache gc failed to prune stale image cache", "error", err)
		} else if len(removed) > 0 {
			slog.Info("agent-compose boxlite cache gc pruned stale image cache", "count", len(removed))
		}
	}
}

func (r *cgoSandboxRuntime) cleanupLegacyBoxliteCaches() {
	if r == nil || r.config == nil {
		return
	}
	removed, err := cleanupLegacyBoxliteImageCaches(r.config.BoxliteHome)
	if err != nil {
		slog.Warn("agent-compose boxlite cache gc failed to prune legacy boxlite caches", "error", err)
		return
	}
	if len(removed) > 0 {
		slog.Info("agent-compose boxlite cache gc pruned legacy boxlite caches", "count", len(removed))
	}
}

func cleanupExpiredCacheDirs(root string, ttl time.Duration, keepIDs map[string]struct{}, now time.Time) ([]string, error) {
	if ttl <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cache root %s: %w", root, err)
	}
	removed := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		if _, keep := keepIDs[name]; keep {
			deleted, err := cleanupExpiredImageCacheArtifacts(filepath.Join(root, name), ttl, now)
			if err != nil {
				return removed, err
			}
			removed = append(removed, deleted...)
			continue
		}
		path := filepath.Join(root, name)
		info, err := entry.Info()
		if err != nil {
			return removed, fmt.Errorf("stat cache dir %s: %w", path, err)
		}
		if now.Sub(info.ModTime()) < ttl {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return removed, fmt.Errorf("remove cache dir %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	return removed, nil
}

func cleanupExpiredImageCacheArtifacts(cacheDir string, ttl time.Duration, now time.Time) ([]string, error) {
	if ttl <= 0 {
		return nil, nil
	}
	removed := make([]string, 0)
	for _, name := range []string{"rootfs", "rootfs.tmp", "oci.tmp"} {
		path := filepath.Join(cacheDir, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("stat cache artifact %s: %w", path, err)
		}
		if now.Sub(info.ModTime()) < ttl {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return removed, fmt.Errorf("remove cache artifact %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	readyFlag := filepath.Join(cacheDir, ".rootfs.ready")
	if info, err := os.Stat(readyFlag); err == nil {
		if now.Sub(info.ModTime()) >= ttl {
			if err := os.Remove(readyFlag); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("remove rootfs ready flag %s: %w", readyFlag, err)
			}
			removed = append(removed, readyFlag)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return removed, fmt.Errorf("stat rootfs ready flag %s: %w", readyFlag, err)
	}
	return removed, nil
}

func hasActiveBoxliteBoxes(dbPath string) (bool, error) {
	state, err := inspectBoxliteActiveState(dbPath)
	if err != nil {
		return false, err
	}
	if !state.known {
		return false, fmt.Errorf("boxlite active state is unknown: %s", state.warning)
	}
	return state.active, nil
}

type boxliteActiveState struct {
	known   bool
	active  bool
	warning string
}

func inspectBoxliteActiveState(dbPath string) (boxliteActiveState, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return boxliteActiveState{warning: "boxlite db path is empty"}, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return boxliteActiveState{warning: fmt.Sprintf("boxlite db %s is missing", dbPath)}, nil
		}
		return boxliteActiveState{}, fmt.Errorf("stat boxlite db %s: %w", dbPath, err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return boxliteActiveState{}, fmt.Errorf("open boxlite db %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var count int
	err = db.QueryRow(`SELECT COUNT(1) FROM box_state WHERE LOWER(TRIM(status)) NOT IN ('', 'stopped', 'exited', 'dead', 'removed')`).Scan(&count)
	if err != nil {
		if isBoxliteUnknownSchemaError(err) {
			return boxliteActiveState{warning: fmt.Sprintf("boxlite db schema is unknown: %v", err)}, nil
		}
		return boxliteActiveState{}, fmt.Errorf("query active boxlite boxes: %w", err)
	}
	return boxliteActiveState{known: true, active: count > 0}, nil
}

func cleanupLegacyBoxliteImageCaches(boxliteHome string) ([]string, error) {
	result, err := pruneBoxliteRuntimeDerivedCaches(context.Background(), boxliteHome, runtimecache.PruneRequest{
		Filter: runtimecache.Filter{
			Driver: runtimecache.DriverBoxLite,
			Domain: runtimecache.DomainRuntimeDerivedCache,
			Type:   runtimecache.CacheTypeRuntime,
		},
		Force: true,
	})
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(result.Removed))
	for _, item := range result.Matched {
		for _, cacheID := range result.Removed {
			if item.CacheID == cacheID {
				removed = append(removed, item.Path)
				break
			}
		}
	}
	return removed, nil
}

func listBoxliteRuntimeDerivedCaches(ctx context.Context, boxliteHome string) (runtimecache.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return runtimecache.ListResult{}, err
	}
	boxliteHome = strings.TrimSpace(boxliteHome)
	if boxliteHome == "" {
		return runtimecache.ListResult{Warnings: []string{"boxlite runtime cache scan skipped: BOXLITE_HOME is empty"}}, nil
	}
	if !filepath.IsAbs(boxliteHome) {
		return runtimecache.ListResult{Warnings: []string{fmt.Sprintf("boxlite runtime cache scan skipped: BOXLITE_HOME %s is not absolute", boxliteHome)}}, nil
	}

	dbPath := filepath.Join(boxliteHome, boxliteDBFileName)
	state, stateWarnings := boxliteInventoryActiveState(dbPath)
	result := runtimecache.ListResult{Warnings: stateWarnings}
	for _, root := range boxliteRuntimeDerivedRoots(boxliteHome) {
		items, warnings := scanBoxliteRuntimeDerivedRoot(ctx, root.path, root.kind, state, dbPath)
		result.Items = append(result.Items, items...)
		result.Warnings = runtimecache.AppendWarnings(result.Warnings, warnings...)
	}
	return result, nil
}

func pruneBoxliteRuntimeDerivedCaches(ctx context.Context, boxliteHome string, req runtimecache.PruneRequest) (runtimecache.Result, error) {
	list, err := listBoxliteRuntimeDerivedCaches(ctx, boxliteHome)
	if err != nil {
		return runtimecache.Result{}, err
	}
	result, err := runtimecache.PruneItems(ctx, list.Items, req, time.Now().UTC(), boxliteRuntimeDerivedRemover{boxliteHome: boxliteHome}.Remove)
	if err != nil {
		return result, err
	}
	result.Warnings = runtimecache.AppendWarnings(list.Warnings, result.Warnings...)
	return result, nil
}

func removeBoxliteRuntimeDerivedCache(ctx context.Context, boxliteHome string, req runtimecache.RemoveRequest) (runtimecache.Result, error) {
	list, err := listBoxliteRuntimeDerivedCaches(ctx, boxliteHome)
	if err != nil {
		return runtimecache.Result{}, err
	}
	result, err := runtimecache.RemoveItem(ctx, list.Items, req, time.Now().UTC(), boxliteRuntimeDerivedRemover{boxliteHome: boxliteHome}.Remove)
	if err != nil {
		return result, err
	}
	result.Warnings = runtimecache.AppendWarnings(list.Warnings, result.Warnings...)
	return result, nil
}

type boxliteRuntimeDerivedRoot struct {
	path string
	kind string
}

func boxliteRuntimeDerivedRoots(boxliteHome string) []boxliteRuntimeDerivedRoot {
	return []boxliteRuntimeDerivedRoot{
		{path: filepath.Join(boxliteHome, "images", "local"), kind: boxliteLocalImageCacheKind},
		{path: filepath.Join(boxliteHome, "images", "disk-images"), kind: boxliteDiskImageCacheKind},
	}
}

func boxliteInventoryActiveState(dbPath string) (boxliteActiveState, []string) {
	state, err := inspectBoxliteActiveState(dbPath)
	if err != nil {
		return boxliteActiveState{warning: err.Error()}, []string{err.Error()}
	}
	if !state.known && strings.TrimSpace(state.warning) != "" {
		return state, []string{state.warning}
	}
	return state, nil
}

func scanBoxliteRuntimeDerivedRoot(ctx context.Context, root, kind string, state boxliteActiveState, dbPath string) ([]runtimecache.Item, []string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read boxlite runtime cache root %s: %v", root, err)}
	}
	items := make([]runtimecache.Item, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, []string{err.Error()}
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			item := boxliteRuntimeDerivedItem(path, kind, nil, state, dbPath)
			item.Warnings = runtimecache.AppendWarnings(item.Warnings, fmt.Sprintf("stat boxlite runtime cache item %s: %v", path, err))
			items = append(items, runtimecache.EvaluateProtection(item, false))
			continue
		}
		items = append(items, boxliteRuntimeDerivedItem(path, kind, info, state, dbPath))
	}
	return items, nil
}

func boxliteRuntimeDerivedItem(path, kind string, info os.FileInfo, state boxliteActiveState, dbPath string) runtimecache.Item {
	size, warnings := runtimecache.EstimateSize(path)
	item := runtimecache.Item{
		Domain:         runtimecache.DomainRuntimeDerivedCache,
		Driver:         runtimecache.DriverBoxLite,
		Kind:           kind,
		Path:           path,
		SizeBytes:      size,
		Status:         runtimecache.StatusOrphaned,
		LastUsedSource: boxliteLastUsedSourceMTime,
		Warnings:       warnings,
	}
	if info != nil {
		item.LastUsedAt = info.ModTime().UTC()
	}
	switch {
	case !state.known:
		item.Status = runtimecache.StatusUnknown
		item.Warnings = runtimecache.AppendWarnings(item.Warnings, state.warning)
	case state.active:
		item.Status = runtimecache.StatusActive
		item.References = []runtimecache.Reference{{
			Type:        boxliteReferenceTypeDB,
			Path:        dbPath,
			Status:      "active",
			Description: "active BoxLite box state exists",
		}}
	default:
		item.Status = runtimecache.StatusOrphaned
	}
	if cacheID, err := runtimecache.GenerateCacheID(item); err == nil {
		item.CacheID = cacheID
	} else {
		item.Status = runtimecache.StatusUnknown
		item.Warnings = runtimecache.AppendWarnings(item.Warnings, err.Error())
	}
	return runtimecache.EvaluateProtection(item, false)
}

type boxliteRuntimeDerivedRemover struct {
	boxliteHome string
}

func (r boxliteRuntimeDerivedRemover) Remove(ctx context.Context, item runtimecache.Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateBoxliteRuntimeDerivedRemoveItem(item); err != nil {
		return err
	}
	expectedID, err := runtimecache.GenerateCacheID(item)
	if err != nil {
		return err
	}
	if item.CacheID != expectedID {
		return fmt.Errorf("%w: cache id does not match inventory item", runtimecache.ErrInvalidCacheID)
	}
	root, err := boxliteRuntimeDerivedRootForKind(r.boxliteHome, item.Kind)
	if err != nil {
		return err
	}

	unlock, err := lockBoxliteRuntimeHome(r.boxliteHome)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()

	safe, err := runtimecache.ValidateCachePath(root, item.Path)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(safe.CanonicalTarget); err != nil {
		return fmt.Errorf("remove boxlite runtime cache item %s: %w", safe.CanonicalTarget, err)
	}
	return nil
}

func validateBoxliteRuntimeDerivedRemoveItem(item runtimecache.Item) error {
	if item.Domain != runtimecache.DomainRuntimeDerivedCache {
		return fmt.Errorf("boxlite runtime cache remover cannot remove domain %q", item.Domain)
	}
	if item.Driver != runtimecache.DriverBoxLite {
		return fmt.Errorf("boxlite runtime cache remover cannot remove driver %q", item.Driver)
	}
	if item.CacheID == "" {
		return fmt.Errorf("%w: cache id is required", runtimecache.ErrInvalidCacheID)
	}
	if _, err := runtimecache.ParseCacheID(item.CacheID); err != nil {
		return err
	}
	switch item.Kind {
	case boxliteLocalImageCacheKind, boxliteDiskImageCacheKind:
		return nil
	default:
		return fmt.Errorf("unsupported boxlite runtime cache kind %q", item.Kind)
	}
}

func boxliteRuntimeDerivedRootForKind(boxliteHome, kind string) (string, error) {
	boxliteHome = strings.TrimSpace(boxliteHome)
	if boxliteHome == "" || !filepath.IsAbs(boxliteHome) {
		return "", fmt.Errorf("BOXLITE_HOME must be an absolute path")
	}
	switch kind {
	case boxliteLocalImageCacheKind:
		return filepath.Join(boxliteHome, "images", "local"), nil
	case boxliteDiskImageCacheKind:
		return filepath.Join(boxliteHome, "images", "disk-images"), nil
	default:
		return "", fmt.Errorf("unsupported boxlite runtime cache kind %q", kind)
	}
}

func lockBoxliteRuntimeHome(boxliteHome string) (func() error, error) {
	boxliteHome = strings.TrimSpace(boxliteHome)
	if boxliteHome == "" || !filepath.IsAbs(boxliteHome) {
		return nil, fmt.Errorf("BOXLITE_HOME must be an absolute path")
	}
	if err := os.MkdirAll(boxliteHome, 0o755); err != nil {
		return nil, fmt.Errorf("ensure boxlite home %s: %w", boxliteHome, err)
	}
	lockPath := filepath.Join(boxliteHome, ".agent-compose-runtime-cache.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open boxlite runtime cache lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock boxlite runtime cache %s: %w", lockPath, err)
	}
	return func() error {
		unlockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		closeErr := lockFile.Close()
		if unlockErr != nil {
			return fmt.Errorf("unlock boxlite runtime cache: %w", unlockErr)
		}
		return closeErr
	}, nil
}

func isBoxliteUnknownSchemaError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") || strings.Contains(message, "no such column")
}
