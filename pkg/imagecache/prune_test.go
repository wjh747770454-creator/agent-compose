package imagecache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneBeforeUsesLastUsedAndProtectsReferencedImage(t *testing.T) {
	cache := newTestCache(t)
	old := writeMaterializeTestImage(t, cache, "team/old:latest")
	metadata, err := cache.LoadMetadata()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	for index := range metadata.Images {
		metadata.Images[index].PulledAt = now.Add(-30 * 24 * time.Hour)
		metadata.Images[index].LastUsedAt = now.Add(-20 * 24 * time.Hour)
	}
	if err := cache.SaveMetadata(metadata); err != nil {
		t.Fatal(err)
	}

	protected, err := cache.PruneBefore(context.Background(), now.Add(-7*24*time.Hour), []string{old.RequestedRef})
	if err != nil {
		t.Fatal(err)
	}
	if protected.Matched != 1 || protected.Skipped != 1 || protected.Removed != 0 {
		t.Fatalf("protected result = %#v", protected)
	}

	removed, err := cache.PruneBefore(context.Background(), now.Add(-7*24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Matched != 1 || removed.Removed != 1 {
		t.Fatalf("removed result = %#v", removed)
	}
	got, err := cache.LoadMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 0 {
		t.Fatalf("remaining metadata = %#v", got.Images)
	}
}

func TestImageUsedAtPrefersLastUsedAndFallsBackToPulled(t *testing.T) {
	cache := newTestCache(t)
	pulled := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	used := pulled.Add(24 * time.Hour)
	if got := cache.imageUsedAt(ImageMetadata{PulledAt: pulled, LastUsedAt: used}); !got.Equal(used) {
		t.Fatalf("imageUsedAt with usage = %s", got)
	}
	if got := cache.imageUsedAt(ImageMetadata{PulledAt: pulled}); !got.Equal(pulled) {
		t.Fatalf("imageUsedAt fallback = %s", got)
	}
}

func TestPruneBeforeRemovesOldUntrackedMaterialization(t *testing.T) {
	cache := newTestCache(t)
	path := cache.MaterializedImageDir("orphan")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	result, err := cache.PruneBefore(context.Background(), time.Now().Add(-24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Removed != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("orphan materialization still exists: %v", err)
	}
}

func TestPruneBeforePreservesMaterializationUsedByRetainedAlias(t *testing.T) {
	cache := newTestCache(t)
	old := writeMaterializeTestImage(t, cache, "team/app:old")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	old.PulledAt = now.Add(-30 * 24 * time.Hour)
	old.LastUsedAt = now.Add(-20 * 24 * time.Hour)
	recent := old
	recent.RequestedRef = "team/app:recent"
	recent.NormalizedRef = "index.docker.io/team/app:recent"
	recent.RepoTags = []string{"team/app:recent"}
	recent.LastUsedAt = now.Add(-time.Hour)
	if err := cache.SaveMetadata(MetadataFile{Images: []ImageMetadata{old, recent}}); err != nil {
		t.Fatal(err)
	}
	materializedPath := cache.MaterializedImageDir(old.ConfigDigest)
	if err := os.MkdirAll(materializedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(materializedPath, "sentinel")
	if err := os.WriteFile(sentinel, []byte("retained"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := cache.PruneBefore(context.Background(), now.Add(-7*24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Removed != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("retained alias materialization was removed: %v", err)
	}
	metadata, err := cache.LoadMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Images) != 1 || metadata.Images[0].RequestedRef != recent.RequestedRef {
		t.Fatalf("remaining metadata = %#v", metadata.Images)
	}
}

func TestPruneBeforePreservesUntrackedMaterializationForMutableSandboxRef(t *testing.T) {
	cache := newTestCache(t)
	old := writeMaterializeTestImage(t, cache, "team/app:latest")
	current := writeMaterializeTestImage(t, cache, "team/app:latest")
	if old.ConfigDigest == current.ConfigDigest {
		t.Fatal("test images unexpectedly share a config digest")
	}
	oldPath := cache.MaterializedImageDir(old.ConfigDigest)
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(oldPath, "sandbox-rootfs")
	if err := os.WriteFile(sentinel, []byte("in use"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	result, err := cache.PruneBefore(context.Background(), time.Now().Add(-24*time.Hour), []string{"team/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 {
		t.Fatalf("cleanup removed protected untracked materialization: %#v", result)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("protected untracked materialization was removed: %v", err)
	}
}
