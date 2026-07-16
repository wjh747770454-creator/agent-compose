package imagecache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/match"
)

type PruneResult struct {
	Matched int
	Removed int
	Skipped int
	Failed  int
}

func (c *Cache) PruneBefore(ctx context.Context, cutoff time.Time, protectedIdentities []string) (PruneResult, error) {
	return c.PruneBeforeWithProtection(ctx, cutoff, func(context.Context) ([]string, error) {
		return protectedIdentities, nil
	})
}

func (c *Cache) PruneBeforeWithProtection(ctx context.Context, cutoff time.Time, protection func(context.Context) ([]string, error)) (result PruneResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return PruneResult{}, err
	}
	if protection == nil {
		return PruneResult{}, fmt.Errorf("image cache protection provider is required")
	}
	unlock, err := c.LockContext(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	defer func() {
		if err := unlock(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	protectedIdentities, err := protection(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	protected := make(map[string]struct{}, len(protectedIdentities))
	for _, identity := range protectedIdentities {
		if value := normalizeLookupValue(identity); value != "" {
			protected[value] = struct{}{}
		}
	}
	metadata, err := c.LoadMetadata()
	if err != nil {
		return PruneResult{}, err
	}
	retainUntrackedMaterializations := requiresConservativeMaterializationRetention(metadata.Images, protected)
	result = PruneResult{}
	remaining := make([]ImageMetadata, 0, len(metadata.Images))
	removed := make([]ImageMetadata, 0)
	for _, image := range metadata.Images {
		usedAt := c.imageUsedAt(image)
		if !usedAt.IsZero() && !usedAt.After(cutoff) {
			result.Matched++
			if imageIsProtected(image, protected) {
				result.Skipped++
				remaining = append(remaining, image)
				continue
			}
			removed = append(removed, image)
			continue
		}
		remaining = append(remaining, image)
	}
	if len(removed) > 0 {
		metadata.Images = remaining
		if err := c.SaveMetadata(metadata); err != nil {
			return result, err
		}
	}
	var joined error
	retainedMaterializations := c.materializationPaths(remaining)
	for _, image := range removed {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(joined, err)
		}
		materializedPath := c.materializedImagePath(image)
		if _, retained := retainedMaterializations[materializedPath]; retained {
			result.Removed++
			continue
		}
		if err := c.removeMaterializedImage(materializedPath); err != nil {
			result.Failed++
			joined = errors.Join(joined, err)
			continue
		}
		result.Removed++
	}
	if err := c.removeUnreferencedManifests(remaining, removed, cutoff); err != nil {
		joined = errors.Join(joined, err)
	}
	if orphanResult, err := c.pruneOldUntrackedPaths(ctx, cutoff, remaining, retainUntrackedMaterializations); err != nil {
		joined = errors.Join(joined, err)
		result.Matched += orphanResult.Matched
		result.Removed += orphanResult.Removed
		result.Failed += orphanResult.Failed
	} else {
		result.Matched += orphanResult.Matched
		result.Removed += orphanResult.Removed
	}
	return result, joined
}

func (c *Cache) imageUsedAt(image ImageMetadata) time.Time {
	if !image.LastUsedAt.IsZero() {
		return image.LastUsedAt.UTC()
	}
	if !image.PulledAt.IsZero() {
		return image.PulledAt.UTC()
	}
	var latest time.Time
	paths := []string{image.RootFSCachePath, image.LayoutCachePath}
	if digest := strings.TrimSpace(image.ManifestDigest); strings.HasPrefix(digest, "sha256:") {
		paths = append(paths, filepath.Join(c.OCILayoutPath(), "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")))
	}
	for _, path := range paths {
		if info, err := os.Stat(strings.TrimSpace(path)); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime().UTC()
		}
	}
	return latest
}

func imageIsProtected(image ImageMetadata, protected map[string]struct{}) bool {
	for _, value := range imageLookupValues(image) {
		if _, ok := protected[normalizeLookupValue(value)]; ok {
			return true
		}
	}
	return false
}

func requiresConservativeMaterializationRetention(images []ImageMetadata, protected map[string]struct{}) bool {
	for identity := range protected {
		if !isImmutableImageIdentity(identity) {
			return true
		}
		resolved := false
		for _, image := range images {
			if imageMatchesLookup(image, identity) {
				resolved = true
				break
			}
		}
		if !resolved {
			return true
		}
	}
	return false
}

func isImmutableImageIdentity(identity string) bool {
	identity = normalizeLookupValue(identity)
	return strings.HasPrefix(identity, "sha256:") || strings.Contains(identity, "@sha256:")
}

func (c *Cache) materializedImagePath(image ImageMetadata) string {
	imageID := firstNonEmpty(image.ConfigDigest, image.CacheKey, image.ManifestDigest, image.NormalizedRef)
	if strings.TrimSpace(imageID) == "" {
		return ""
	}
	return filepath.Clean(c.MaterializedImageDir(imageID))
}

func (c *Cache) materializationPaths(images []ImageMetadata) map[string]struct{} {
	paths := make(map[string]struct{}, len(images))
	for _, image := range images {
		if path := c.materializedImagePath(image); path != "" {
			paths[path] = struct{}{}
		}
	}
	return paths
}

func (c *Cache) removeMaterializedImage(target string) error {
	if target == "" {
		return nil
	}
	root, err := filepath.Abs(c.MaterializationRoot())
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if filepath.Dir(abs) != root {
		return fmt.Errorf("materialized image path %q is outside cache root", abs)
	}
	return os.RemoveAll(abs)
}

func (c *Cache) removeUnreferencedManifests(images, removed []ImageMetadata, cutoff time.Time) error {
	if _, err := os.Stat(filepath.Join(c.OCILayoutPath(), "index.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	keep := make(map[string]struct{}, len(images))
	for _, image := range images {
		if digest := strings.TrimSpace(image.ManifestDigest); digest != "" {
			keep[digest] = struct{}{}
		}
	}
	expired := make(map[string]struct{}, len(removed))
	for _, image := range removed {
		if digest := strings.TrimSpace(image.ManifestDigest); digest != "" {
			expired[digest] = struct{}{}
		}
	}
	path := layout.Path(c.OCILayoutPath())
	index, err := path.ImageIndex()
	if err != nil {
		return err
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return err
	}
	for _, descriptor := range manifest.Manifests {
		if _, ok := keep[descriptor.Digest.String()]; ok {
			continue
		}
		if _, explicitlyExpired := expired[descriptor.Digest.String()]; !explicitlyExpired {
			info, statErr := os.Stat(filepath.Join(c.OCILayoutPath(), "blobs", descriptor.Digest.Algorithm, descriptor.Digest.Hex))
			if statErr != nil || info.ModTime().After(cutoff) {
				continue
			}
		}
		digest, err := v1.NewHash(descriptor.Digest.String())
		if err != nil {
			return err
		}
		if err := path.RemoveDescriptors(match.Digests(digest)); err != nil {
			return err
		}
	}
	orphans, err := path.GarbageCollect()
	if err != nil {
		return err
	}
	for _, orphan := range orphans {
		if err := path.RemoveBlob(orphan); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (c *Cache) pruneOldUntrackedPaths(ctx context.Context, cutoff time.Time, images []ImageMetadata, retainUntrackedMaterializations bool) (PruneResult, error) {
	tracked := make(map[string]struct{}, len(images))
	for _, image := range images {
		imageID := firstNonEmpty(image.ConfigDigest, image.CacheKey, image.ManifestDigest, image.NormalizedRef)
		if imageID != "" {
			tracked[filepath.Base(c.MaterializedImageDir(imageID))] = struct{}{}
		}
	}
	result := PruneResult{}
	var joined error
	for _, root := range []string{filepath.Join(c.Root(), "tmp"), c.MaterializationRoot()} {
		if root == c.MaterializationRoot() && retainUntrackedMaterializations {
			continue
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return result, errors.Join(joined, err)
			}
			if root == c.MaterializationRoot() {
				if _, ok := tracked[entry.Name()]; ok {
					continue
				}
			}
			info, err := entry.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			result.Matched++
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				result.Failed++
				joined = errors.Join(joined, err)
				continue
			}
			result.Removed++
		}
	}
	return result, joined
}
