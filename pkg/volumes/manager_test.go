package volumes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	domain "agent-compose/pkg/model"
)

type fakeStore struct {
	items map[string]domain.VolumeRecord
}

func (s *fakeStore) CreateVolume(_ context.Context, item domain.VolumeRecord) (domain.VolumeRecord, error) {
	if s.items == nil {
		s.items = make(map[string]domain.VolumeRecord)
	}
	s.items[item.ID] = item
	s.items[item.Name] = item
	return item, nil
}

func (s *fakeStore) UpdateVolume(_ context.Context, item domain.VolumeRecord) (domain.VolumeRecord, error) {
	s.items[item.ID] = item
	s.items[item.Name] = item
	return item, nil
}

func (s *fakeStore) GetVolume(_ context.Context, key string) (domain.VolumeRecord, error) {
	item, ok := s.items[key]
	if !ok {
		return domain.VolumeRecord{}, domain.ResourceError(domain.ErrNotFound, "volume", key, "not found", nil)
	}
	return item, nil
}

func (s *fakeStore) GetVolumeIfExists(_ context.Context, key string) (domain.VolumeRecord, bool, error) {
	item, ok := s.items[key]
	return item, ok, nil
}

func (s *fakeStore) ListVolumes(context.Context, domain.VolumeListOptions) ([]domain.VolumeRecord, error) {
	seen := map[string]struct{}{}
	var items []domain.VolumeRecord
	for key, item := range s.items {
		if key != item.ID {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		items = append(items, item)
	}
	return items, nil
}

func (s *fakeStore) RemoveVolume(_ context.Context, key string) error {
	item := s.items[key]
	delete(s.items, item.ID)
	delete(s.items, item.Name)
	return nil
}

func (s *fakeStore) FindVolumeConfigReferences(context.Context, string) ([]domain.VolumeReference, error) {
	return nil, nil
}

func TestManagerResolveBindAndNamedVolumeMounts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bindDir := filepath.Join(root, "fixtures")
	if err := os.MkdirAll(bindDir, 0o755); err != nil {
		t.Fatalf("mkdir bind dir: %v", err)
	}
	dataRoot := t.TempDir()
	store := &fakeStore{}
	manager := NewManager(store, LocalDriver{DataRoot: dataRoot})
	created, err := manager.Create(ctx, domain.VolumeRecord{Name: "cache"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := uuid.Parse(created.ID); err != nil || strings.Contains(created.ID, "cache") {
		t.Fatalf("volume internal id = %q, want opaque UUID not derived from name", created.ID)
	}
	mounts, warnings, err := manager.ResolveMounts(ctx, []domain.VolumeMountSpec{
		{Type: domain.VolumeMountTypeBind, Source: "./fixtures", Target: "/fixtures", ReadOnly: true},
		{Type: domain.VolumeMountTypeVolume, Source: "cache", Target: "/cache"},
	}, ResolveOptions{ProjectRoot: root})
	if err != nil {
		t.Fatalf("ResolveMounts: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(mounts) != 2 {
		t.Fatalf("mounts = %#v", mounts)
	}
	if mounts[0].HostPath != bindDir || !mounts[0].ReadOnly {
		t.Fatalf("bind mount = %#v", mounts[0])
	}
	if !strings.HasPrefix(mounts[0].ID, "mount-") || len(mounts[0].ID) != len("mount-")+24 || strings.Contains(mounts[0].ID, "fixtures") {
		t.Fatalf("bind mount id = %q, want opaque stable hash id", mounts[0].ID)
	}
	if mounts[1].VolumeID != created.ID || mounts[1].Target != "/cache" {
		t.Fatalf("volume mount = %#v", mounts[1])
	}
	if !strings.HasPrefix(mounts[1].ID, "mount-") || len(mounts[1].ID) != len("mount-")+24 || strings.Contains(mounts[1].ID, "cache") {
		t.Fatalf("volume mount id = %q, want opaque stable hash id", mounts[1].ID)
	}
	if _, err := os.Stat(mounts[1].HostPath); err != nil {
		t.Fatalf("volume host path missing: %v", err)
	}
}

func TestManagerResolveNamedVolumeMultipleTargetsNestedAndReadOnly(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	manager := NewManager(store, LocalDriver{DataRoot: t.TempDir()})
	shared, err := manager.Create(ctx, domain.VolumeRecord{Name: "shared-cache"})
	if err != nil {
		t.Fatalf("Create shared-cache: %v", err)
	}
	nested, err := manager.Create(ctx, domain.VolumeRecord{Name: "nested-cache"})
	if err != nil {
		t.Fatalf("Create nested-cache: %v", err)
	}
	readonly, err := manager.Create(ctx, domain.VolumeRecord{Name: "readonly-cache"})
	if err != nil {
		t.Fatalf("Create readonly-cache: %v", err)
	}
	mounts, warnings, err := manager.ResolveMounts(ctx, []domain.VolumeMountSpec{
		{Source: "shared-cache", Target: "/mnt/shared-a"},
		{Source: "shared-cache", Target: "/mnt/shared-b"},
		{Source: "nested-cache", Target: "/mnt/nested/parent/child"},
		{Source: "readonly-cache", Target: "/mnt/readonly", ReadOnly: true},
	}, ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveMounts: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	got := map[string]domain.SessionVolumeMount{}
	for _, mount := range mounts {
		got[mount.Target] = mount
		if !strings.HasPrefix(mount.ID, "mount-") || len(mount.ID) != len("mount-")+24 {
			t.Fatalf("mount id = %q, want stable hash id", mount.ID)
		}
	}
	if len(got) != 4 {
		t.Fatalf("mount targets = %#v, want 4 distinct targets", got)
	}
	if got["/mnt/shared-a"].VolumeID != shared.ID || got["/mnt/shared-b"].VolumeID != shared.ID {
		t.Fatalf("shared mounts = %#v %#v, want same volume id %s", got["/mnt/shared-a"], got["/mnt/shared-b"], shared.ID)
	}
	if got["/mnt/shared-a"].HostPath != got["/mnt/shared-b"].HostPath {
		t.Fatalf("shared host paths differ: %q vs %q", got["/mnt/shared-a"].HostPath, got["/mnt/shared-b"].HostPath)
	}
	if got["/mnt/shared-a"].ID == got["/mnt/shared-b"].ID {
		t.Fatalf("same source with different targets should have different mount ids: %#v %#v", got["/mnt/shared-a"], got["/mnt/shared-b"])
	}
	if got["/mnt/nested/parent/child"].VolumeID != nested.ID {
		t.Fatalf("nested mount = %#v, want volume id %s", got["/mnt/nested/parent/child"], nested.ID)
	}
	if got["/mnt/readonly"].VolumeID != readonly.ID || !got["/mnt/readonly"].ReadOnly {
		t.Fatalf("readonly mount = %#v, want readonly volume id %s", got["/mnt/readonly"], readonly.ID)
	}
}

func TestManagerResolveProjectVolumeMappingTakesPrecedence(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	globalStore := &fakeStore{}
	manager := NewManager(globalStore, LocalDriver{DataRoot: dataRoot})
	global, err := manager.Create(ctx, domain.VolumeRecord{Name: "cache"})
	if err != nil {
		t.Fatalf("Create global cache: %v", err)
	}
	projectPath := filepath.Join(dataRoot, "project-cache")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project path: %v", err)
	}
	project := domain.VolumeRecord{
		ID:        "project-volume-id",
		Name:      "project_cache",
		Driver:    domain.VolumeDriverLocal,
		Path:      projectPath,
		ProjectID: "project-1",
	}
	mounts, warnings, err := manager.ResolveMounts(ctx, []domain.VolumeMountSpec{
		{Source: "cache", Target: "/cache"},
	}, ResolveOptions{ProjectVolumes: map[string]domain.VolumeRecord{"cache": project}})
	if err != nil {
		t.Fatalf("ResolveMounts: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(mounts) != 1 {
		t.Fatalf("mounts = %#v, want one", mounts)
	}
	if mounts[0].VolumeID != project.ID || mounts[0].HostPath != projectPath {
		t.Fatalf("mount = %#v, want project volume %s path %s; global was %s", mounts[0], project.ID, projectPath, global.ID)
	}
}

func TestManagerResolveMountsReportsWarningsAndMissingVolumes(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	manager := NewManager(store, LocalDriver{DataRoot: t.TempDir()})
	created, err := manager.Create(ctx, domain.VolumeRecord{Name: "cache"})
	if err != nil {
		t.Fatalf("Create cache: %v", err)
	}
	mounts, warnings, err := manager.ResolveMounts(ctx, []domain.VolumeMountSpec{
		{Source: "cache", Target: "/data/cache"},
		{Source: "cache", Target: "/root/.cache"},
	}, ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveMounts warnings case: %v", err)
	}
	if len(mounts) != 2 || mounts[0].VolumeID != created.ID || mounts[1].VolumeID != created.ID {
		t.Fatalf("mounts = %#v", mounts)
	}
	if len(warnings) != 2 ||
		!strings.Contains(warnings[0], "/data/cache") ||
		!strings.Contains(warnings[1], "/root/.cache") {
		t.Fatalf("warnings = %#v, want reserved target warnings", warnings)
	}
	if _, _, err := manager.ResolveMounts(ctx, []domain.VolumeMountSpec{
		{Source: "missing-cache", Target: "/cache"},
	}, ResolveOptions{}); err == nil {
		t.Fatal("ResolveMounts missing volume returned nil error")
	}
}

func TestBindResolverResolvesRelativeAbsoluteAndSymlinkDirectories(t *testing.T) {
	root := t.TempDir()
	relativeDir := filepath.Join(root, "relative")
	absoluteDir := filepath.Join(root, "absolute")
	if err := os.MkdirAll(relativeDir, 0o755); err != nil {
		t.Fatalf("mkdir relative dir: %v", err)
	}
	if err := os.MkdirAll(absoluteDir, 0o755); err != nil {
		t.Fatalf("mkdir absolute dir: %v", err)
	}
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(relativeDir, linkPath); err != nil {
		t.Fatalf("symlink relative dir: %v", err)
	}
	resolver := BindResolver{ProjectRoot: root}
	if got, err := resolver.Resolve("./relative"); err != nil || got != relativeDir {
		t.Fatalf("Resolve relative = %q err=%v, want %q", got, err, relativeDir)
	}
	if got, err := resolver.Resolve(absoluteDir); err != nil || got != absoluteDir {
		t.Fatalf("Resolve absolute = %q err=%v, want %q", got, err, absoluteDir)
	}
	if got, err := resolver.Resolve("./link"); err != nil || got != relativeDir {
		t.Fatalf("Resolve symlink = %q err=%v, want evaluated %q", got, err, relativeDir)
	}
}

func TestManagerListAndPruneVolumes(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	manager := NewManager(store, LocalDriver{DataRoot: t.TempDir()})
	if _, err := manager.Create(ctx, domain.VolumeRecord{Name: "cache"}); err != nil {
		t.Fatalf("Create cache: %v", err)
	}
	if _, err := manager.Create(ctx, domain.VolumeRecord{Name: "state"}); err != nil {
		t.Fatalf("Create state: %v", err)
	}
	listed, err := manager.List(ctx, domain.VolumeListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %#v", listed)
	}
	dryRun, err := manager.Prune(ctx, domain.VolumeListOptions{}, false)
	if err != nil {
		t.Fatalf("Prune dry-run: %v", err)
	}
	if !dryRun.DryRun || len(dryRun.Matched) != 2 || len(dryRun.Removed) != 0 {
		t.Fatalf("dry-run prune = %#v", dryRun)
	}
	pruned, err := manager.Prune(ctx, domain.VolumeListOptions{}, true)
	if err != nil {
		t.Fatalf("Prune force: %v", err)
	}
	if pruned.DryRun || len(pruned.Removed) != 2 {
		t.Fatalf("force prune = %#v", pruned)
	}
	if listed, err := manager.List(ctx, domain.VolumeListOptions{}); err != nil || len(listed) != 0 {
		t.Fatalf("listed after prune = %#v err=%v", listed, err)
	}
}

func TestBindResolverRejectsMissingOrFileSource(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	resolver := BindResolver{ProjectRoot: root}
	if _, err := resolver.Resolve("./missing"); err == nil {
		t.Fatal("Resolve missing returned nil")
	}
	if _, err := resolver.Resolve("./file.txt"); err == nil {
		t.Fatal("Resolve file returned nil")
	}
}
