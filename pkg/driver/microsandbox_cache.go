//go:build cgo

package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agent-compose/pkg/runtimecache"
)

const (
	microsandboxDockerDiskKind       = "microsandbox-docker-disk"
	microsandboxSandboxStateKind     = "microsandbox-sandbox-state"
	microsandboxLastUsedSourceMTime  = "mtime"
	microsandboxReferenceTypeSandbox = "sandbox"
)

type microsandboxCacheReferenceState struct {
	ActiveSandboxes     map[string]runtimecache.Reference
	ReferencedSandboxes map[string]runtimecache.Reference
	Unknown             bool
	Warnings            []string
}

func listMicrosandboxSandboxEphemeralCaches(ctx context.Context, microsandboxHome string, refs microsandboxCacheReferenceState) (runtimecache.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return runtimecache.ListResult{}, err
	}
	microsandboxHome = strings.TrimSpace(microsandboxHome)
	if microsandboxHome == "" {
		return runtimecache.ListResult{Warnings: []string{"microsandbox sandbox cache scan skipped: MICROSANDBOX_HOME is empty"}}, nil
	}
	if !filepath.IsAbs(microsandboxHome) {
		return runtimecache.ListResult{Warnings: []string{fmt.Sprintf("microsandbox sandbox cache scan skipped: MICROSANDBOX_HOME %s is not absolute", microsandboxHome)}}, nil
	}
	result := runtimecache.ListResult{Warnings: runtimecache.AppendWarnings(nil, refs.Warnings...)}
	for _, root := range microsandboxSandboxEphemeralRoots(microsandboxHome) {
		items, warnings := scanMicrosandboxSandboxEphemeralRoot(ctx, root.path, root.kind, refs)
		result.Items = append(result.Items, items...)
		result.Warnings = runtimecache.AppendWarnings(result.Warnings, warnings...)
	}
	return result, nil
}

func pruneMicrosandboxSandboxEphemeralCaches(ctx context.Context, microsandboxHome string, refs microsandboxCacheReferenceState, req runtimecache.PruneRequest) (runtimecache.Result, error) {
	list, err := listMicrosandboxSandboxEphemeralCaches(ctx, microsandboxHome, refs)
	if err != nil {
		return runtimecache.Result{}, err
	}
	result, err := runtimecache.PruneItems(ctx, list.Items, req, time.Now().UTC(), microsandboxSandboxEphemeralRemover{microsandboxHome: microsandboxHome}.Remove)
	if err != nil {
		return result, err
	}
	result.Warnings = runtimecache.AppendWarnings(list.Warnings, result.Warnings...)
	return result, nil
}

func removeMicrosandboxSandboxEphemeralCache(ctx context.Context, microsandboxHome string, refs microsandboxCacheReferenceState, req runtimecache.RemoveRequest) (runtimecache.Result, error) {
	list, err := listMicrosandboxSandboxEphemeralCaches(ctx, microsandboxHome, refs)
	if err != nil {
		return runtimecache.Result{}, err
	}
	result, err := runtimecache.RemoveItem(ctx, list.Items, req, time.Now().UTC(), microsandboxSandboxEphemeralRemover{microsandboxHome: microsandboxHome}.Remove)
	if err != nil {
		return result, err
	}
	result.Warnings = runtimecache.AppendWarnings(list.Warnings, result.Warnings...)
	return result, nil
}

type microsandboxSandboxEphemeralRoot struct {
	path string
	kind string
}

func microsandboxSandboxEphemeralRoots(microsandboxHome string) []microsandboxSandboxEphemeralRoot {
	return []microsandboxSandboxEphemeralRoot{
		{path: filepath.Join(microsandboxHome, "docker-disks"), kind: microsandboxDockerDiskKind},
		{path: filepath.Join(microsandboxHome, "sandboxes"), kind: microsandboxSandboxStateKind},
	}
}

func scanMicrosandboxSandboxEphemeralRoot(ctx context.Context, root, kind string, refs microsandboxCacheReferenceState) ([]runtimecache.Item, []string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read microsandbox sandbox cache root %s: %v", root, err)}
	}
	items := make([]runtimecache.Item, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, []string{err.Error()}
		}
		if kind == microsandboxDockerDiskKind && (entry.IsDir() || !strings.HasSuffix(entry.Name(), ".raw")) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			item := microsandboxSandboxEphemeralItem(path, kind, nil, refs)
			item.Warnings = runtimecache.AppendWarnings(item.Warnings, fmt.Sprintf("stat microsandbox sandbox cache item %s: %v", path, err))
			items = append(items, runtimecache.EvaluateProtection(item, false))
			continue
		}
		items = append(items, microsandboxSandboxEphemeralItem(path, kind, info, refs))
	}
	return items, nil
}

func microsandboxSandboxEphemeralItem(path, kind string, info os.FileInfo, refs microsandboxCacheReferenceState) runtimecache.Item {
	size, warnings := runtimecache.EstimateSize(path)
	item := runtimecache.Item{
		Domain:         runtimecache.DomainSandboxEphemeralState,
		Driver:         runtimecache.DriverMicrosandbox,
		Kind:           kind,
		Path:           path,
		SizeBytes:      size,
		Status:         runtimecache.StatusOrphaned,
		LastUsedSource: microsandboxLastUsedSourceMTime,
		Warnings:       runtimecache.AppendWarnings(warnings, refs.Warnings...),
	}
	if info != nil {
		item.LastUsedAt = info.ModTime().UTC()
	}
	switch kind {
	case microsandboxDockerDiskKind:
		item.SandboxID = strings.TrimSuffix(filepath.Base(path), ".raw")
		applyMicrosandboxDockerDiskReferenceState(&item, refs.ActiveSandboxes, refs.ReferencedSandboxes, item.SandboxID)
	case microsandboxSandboxStateKind:
		item.SandboxID = filepath.Base(path)
		applyMicrosandboxReferenceState(&item, refs.ActiveSandboxes, refs.ReferencedSandboxes, item.SandboxID, microsandboxReferenceTypeSandbox)
	default:
		item.Status = runtimecache.StatusUnknown
		item.Warnings = runtimecache.AppendWarnings(item.Warnings, fmt.Sprintf("unsupported microsandbox sandbox cache kind %q", kind))
	}
	if refs.Unknown {
		item.Status = runtimecache.StatusUnknown
		item.Warnings = runtimecache.AppendWarnings(item.Warnings, "microsandbox sandbox reference state is unknown")
	}
	if cacheID, err := runtimecache.GenerateCacheID(item); err == nil {
		item.CacheID = cacheID
	} else {
		item.Status = runtimecache.StatusUnknown
		item.Warnings = runtimecache.AppendWarnings(item.Warnings, err.Error())
	}
	return runtimecache.EvaluateProtection(item, false)
}

func applyMicrosandboxDockerDiskReferenceState(item *runtimecache.Item, active, referenced map[string]runtimecache.Reference, diskName string) {
	if id, ref, ok := microsandboxDockerDiskReference(active, diskName); ok {
		item.SandboxID = id
		applyMicrosandboxReference(&ref, id, microsandboxReferenceTypeSandbox, "active")
		item.Status = runtimecache.StatusActive
		item.References = []runtimecache.Reference{ref}
		return
	}
	if id, ref, ok := microsandboxDockerDiskReference(referenced, diskName); ok {
		item.SandboxID = id
		applyMicrosandboxReference(&ref, id, microsandboxReferenceTypeSandbox, "stopped")
		item.Status = runtimecache.StatusReferenced
		item.References = []runtimecache.Reference{ref}
		return
	}
	item.Status = runtimecache.StatusOrphaned
}

func microsandboxDockerDiskReference(refs map[string]runtimecache.Reference, diskName string) (string, runtimecache.Reference, bool) {
	if ref, ok := refs[diskName]; ok {
		return diskName, ref, true
	}
	for id, ref := range refs {
		if microsandboxDockerDiskName(id) == diskName {
			return id, ref, true
		}
	}
	return "", runtimecache.Reference{}, false
}

func applyMicrosandboxReferenceState(item *runtimecache.Item, active, referenced map[string]runtimecache.Reference, id, refType string) {
	if ref, ok := active[id]; ok {
		applyMicrosandboxReference(&ref, id, refType, "active")
		item.Status = runtimecache.StatusActive
		item.References = []runtimecache.Reference{ref}
		return
	}
	if ref, ok := referenced[id]; ok {
		applyMicrosandboxReference(&ref, id, refType, "stopped")
		item.Status = runtimecache.StatusReferenced
		item.References = []runtimecache.Reference{ref}
		return
	}
	item.Status = runtimecache.StatusOrphaned
}

func applyMicrosandboxReference(ref *runtimecache.Reference, id, refType, status string) {
	ref.Type = firstNonEmpty(ref.Type, refType)
	ref.ID = firstNonEmpty(ref.ID, id)
	ref.Status = firstNonEmpty(ref.Status, status)
}

type microsandboxSandboxEphemeralRemover struct {
	microsandboxHome string
}

func (r microsandboxSandboxEphemeralRemover) Remove(ctx context.Context, item runtimecache.Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMicrosandboxSandboxEphemeralRemoveItem(item); err != nil {
		return err
	}
	expectedID, err := runtimecache.GenerateCacheID(item)
	if err != nil {
		return err
	}
	if item.CacheID != expectedID {
		return fmt.Errorf("%w: cache id does not match inventory item", runtimecache.ErrInvalidCacheID)
	}
	root, err := microsandboxSandboxEphemeralRootForKind(r.microsandboxHome, item.Kind)
	if err != nil {
		return err
	}

	unlock, err := lockMicrosandboxRuntimeHome(r.microsandboxHome)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()

	safe, err := runtimecache.ValidateCachePath(root, item.Path)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(safe.CanonicalTarget); err != nil {
		return fmt.Errorf("remove microsandbox sandbox cache item %s: %w", safe.CanonicalTarget, err)
	}
	return nil
}

func validateMicrosandboxSandboxEphemeralRemoveItem(item runtimecache.Item) error {
	if item.Domain != runtimecache.DomainSandboxEphemeralState {
		return fmt.Errorf("microsandbox sandbox cache remover cannot remove domain %q", item.Domain)
	}
	if item.Driver != runtimecache.DriverMicrosandbox {
		return fmt.Errorf("microsandbox sandbox cache remover cannot remove driver %q", item.Driver)
	}
	if item.CacheID == "" {
		return fmt.Errorf("%w: cache id is required", runtimecache.ErrInvalidCacheID)
	}
	if _, err := runtimecache.ParseCacheID(item.CacheID); err != nil {
		return err
	}
	switch item.Kind {
	case microsandboxDockerDiskKind, microsandboxSandboxStateKind:
		return nil
	default:
		return fmt.Errorf("unsupported microsandbox sandbox cache kind %q", item.Kind)
	}
}

func microsandboxSandboxEphemeralRootForKind(microsandboxHome, kind string) (string, error) {
	microsandboxHome = strings.TrimSpace(microsandboxHome)
	if microsandboxHome == "" || !filepath.IsAbs(microsandboxHome) {
		return "", fmt.Errorf("MICROSANDBOX_HOME must be an absolute path")
	}
	switch kind {
	case microsandboxDockerDiskKind:
		return filepath.Join(microsandboxHome, "docker-disks"), nil
	case microsandboxSandboxStateKind:
		return filepath.Join(microsandboxHome, "sandboxes"), nil
	default:
		return "", fmt.Errorf("unsupported microsandbox sandbox cache kind %q", kind)
	}
}

func lockMicrosandboxRuntimeHome(microsandboxHome string) (func() error, error) {
	microsandboxHome = strings.TrimSpace(microsandboxHome)
	if microsandboxHome == "" || !filepath.IsAbs(microsandboxHome) {
		return nil, fmt.Errorf("MICROSANDBOX_HOME must be an absolute path")
	}
	if err := os.MkdirAll(microsandboxHome, 0o755); err != nil {
		return nil, fmt.Errorf("ensure microsandbox home %s: %w", microsandboxHome, err)
	}
	lockPath := filepath.Join(microsandboxHome, ".agent-compose-sandbox-cache.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open microsandbox sandbox cache lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock microsandbox sandbox cache %s: %w", lockPath, err)
	}
	return func() error {
		unlockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		closeErr := lockFile.Close()
		if unlockErr != nil {
			return fmt.Errorf("unlock microsandbox sandbox cache: %w", unlockErr)
		}
		return closeErr
	}, nil
}
