package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-compose/pkg/cleanup"
	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/imagecache"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sessions"
	"agent-compose/pkg/storage/sessionstore"
)

func TestImageCacheCleanerProtectsResumableSandboxAndReleasesReclaimedSandbox(t *testing.T) {
	root := t.TempDir()
	sandboxID := "sandbox-1"
	if err := os.MkdirAll(filepath.Join(root, sandboxID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sessions.WriteOwnershipRecord(root, sessions.OwnershipRecord{
		SandboxID: sandboxID, SandboxPath: filepath.Join(root, sandboxID), LifecycleState: "active",
		CacheDependencies: []sessions.CacheDependency{{Domain: "runtime-image", Identity: "sha256:guest"}},
	}); err != nil {
		t.Fatal(err)
	}
	sandbox := &domain.Sandbox{Summary: domain.SandboxSummary{ID: sandboxID, GuestImage: "guest:latest"}}
	cleaner := &ImageCacheCleaner{Sandboxes: cleanupSandboxStoreFake{sandboxes: []*domain.Sandbox{sandbox}}, SandboxRoot: root}
	protected, err := cleaner.protectedIdentities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(protected, "guest:latest") || !slices.Contains(protected, "sha256:guest") {
		t.Fatalf("protected identities = %v", protected)
	}

	sandbox.WorkspaceReclamation = &domain.SandboxWorkspaceReclamation{
		State: domain.SandboxWorkspaceReclamationStateReclaimed, CompletedAt: time.Now(),
	}
	protected, err = cleaner.protectedIdentities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(protected) != 0 {
		t.Fatalf("reclaimed sandbox protected identities = %v", protected)
	}
}

func TestImageCacheCleanerSkipsOnlyConfirmedOrphanOwnership(t *testing.T) {
	root := t.TempDir()
	for _, sandboxID := range []string{"orphan", "unreadable"} {
		if err := sessions.WriteOwnershipRecord(root, sessions.OwnershipRecord{
			SandboxID: sandboxID, SandboxPath: filepath.Join(root, sandboxID), LifecycleState: "active",
			CacheDependencies: []sessions.CacheDependency{{Domain: "runtime-image", Identity: sandboxID + ":latest"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "unreadable"), 0o755); err != nil {
		t.Fatal(err)
	}

	cleaner := &ImageCacheCleaner{Sandboxes: cleanupSandboxStoreFake{}, SandboxRoot: root}
	protected, err := cleaner.protectedIdentities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(protected, "orphan:latest") || !slices.Contains(protected, "unreadable:latest") {
		t.Fatalf("protected identities = %v, want only existing unreadable sandbox dependency", protected)
	}
}

func TestOwnershipPathPresentReportsInspectedPath(t *testing.T) {
	path := "/sandboxes/unreadable"
	inspectErr := errors.New("permission denied")
	_, err := ownershipPathPresent(path, func(got string) (os.FileInfo, error) {
		if got != path {
			t.Fatalf("inspected path = %q, want %q", got, path)
		}
		return nil, inspectErr
	})
	if err == nil || !errors.Is(err, inspectErr) || !strings.Contains(err.Error(), path) {
		t.Fatalf("ownership path error = %v, want wrapped error containing %q", err, path)
	}
}

func TestImageCacheCleanerSerializesProtectionSnapshotWithSandboxRegistration(t *testing.T) {
	root := t.TempDir()
	cache, err := imagecache.New(imagecache.Config{Root: filepath.Join(root, "images")})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	if err := cache.SaveMetadata(imagecache.MetadataFile{Images: []imagecache.ImageMetadata{{
		RequestedRef: "guest:latest", NormalizedRef: "index.docker.io/library/guest:latest",
		ConfigDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PulledAt:     now.Add(-30 * 24 * time.Hour), LastUsedAt: now.Add(-20 * 24 * time.Hour),
	}}}); err != nil {
		t.Fatal(err)
	}
	config := &appconfig.Config{
		DataRoot: root, SandboxRoot: filepath.Join(root, "sandboxes"), RuntimeDriver: "docker",
		DefaultImage: "guest:latest", DockerDefaultImage: "guest:latest", JupyterProxyBasePath: "/jupyter",
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	registration := &blockingCacheRegistration{
		cache: cache, acquired: make(chan struct{}), release: make(chan struct{}),
	}
	releaseRegistration := sync.OnceFunc(func() { close(registration.release) })
	defer releaseRegistration()
	store.SetCacheDependencyLocker(registration)

	createDone := make(chan error, 1)
	go func() {
		_, createErr := store.CreateSandbox(context.Background(), "race", "", "docker", "guest:latest", "", "test", nil, nil, nil)
		createDone <- createErr
	}()
	<-registration.acquired

	cleaner := &ImageCacheCleaner{Cache: cache, Sandboxes: store, SandboxRoot: config.SandboxRoot}
	cleanDone := make(chan struct {
		result cleanup.Result
		err    error
	}, 1)
	cleanStarted := make(chan struct{})
	go func() {
		close(cleanStarted)
		result, cleanErr := cleaner.Clean(context.Background(), now.Add(-7*24*time.Hour))
		cleanDone <- struct {
			result cleanup.Result
			err    error
		}{result: result, err: cleanErr}
	}()
	<-cleanStarted
	select {
	case outcome := <-cleanDone:
		t.Fatalf("cleanup bypassed sandbox registration lock: %#v, %v", outcome.result, outcome.err)
	case <-time.After(250 * time.Millisecond):
	}
	releaseRegistration()
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	outcome := <-cleanDone
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.result.Matched != 1 || outcome.result.Skipped != 1 || outcome.result.Removed != 0 {
		t.Fatalf("cleanup result after registration = %#v", outcome.result)
	}
}

type cleanupSandboxStoreFake struct {
	sandboxes []*domain.Sandbox
}

type blockingCacheRegistration struct {
	cache    *imagecache.Cache
	acquired chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (l *blockingCacheRegistration) WithLockContext(ctx context.Context, fn func() error) error {
	return l.cache.WithLockContext(ctx, func() error {
		l.once.Do(func() { close(l.acquired) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.release:
			return fn()
		}
	})
}

func (f cleanupSandboxStoreFake) ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error) {
	return domain.SandboxListResult{Sandboxes: f.sandboxes}, nil
}
