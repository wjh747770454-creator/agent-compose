package adapters

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"agent-compose/pkg/cleanup"
	"agent-compose/pkg/imagecache"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sessions"
)

type cleanupSandboxStore interface {
	ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error)
}

type ImageCacheCleaner struct {
	Cache       *imagecache.Cache
	Sandboxes   cleanupSandboxStore
	SandboxRoot string
}

func (c *ImageCacheCleaner) Name() string { return "image-cache" }

func (c *ImageCacheCleaner) Clean(ctx context.Context, cutoff time.Time) (cleanup.Result, error) {
	result, err := c.Cache.PruneBeforeWithProtection(ctx, cutoff, c.protectedIdentities)
	return cleanup.Result{
		Matched: result.Matched,
		Removed: result.Removed,
		Skipped: result.Skipped,
		Failed:  result.Failed,
	}, err
}

func (c *ImageCacheCleaner) protectedIdentities(ctx context.Context) ([]string, error) {
	listed, err := c.Sandboxes.ListSandboxes(ctx, domain.SandboxListOptions{Limit: 1 << 30})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*domain.Sandbox, len(listed.Sandboxes))
	for _, sandbox := range listed.Sandboxes {
		byID[sandbox.Summary.ID] = sandbox
	}
	records, warnings := sessions.ListOwnershipRecords(c.SandboxRoot)
	if len(warnings) > 0 {
		return nil, fmt.Errorf("sandbox ownership inventory is incomplete: %s", strings.Join(warnings, "; "))
	}
	protected := make([]string, 0, len(records))
	for _, sandbox := range listed.Sandboxes {
		if !domain.SandboxWorkspaceReclaimed(sandbox) && sandbox.Summary.GuestImage != "" {
			protected = append(protected, sandbox.Summary.GuestImage)
		}
	}
	for _, record := range records {
		if sandbox := byID[record.SandboxID]; sandbox != nil {
			if domain.SandboxWorkspaceReclaimed(sandbox) {
				continue
			}
		} else if _, err := os.Lstat(record.SandboxPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("inspect sandbox ownership path %s: %w", record.SandboxID, err)
		}
		for _, dependency := range record.CacheDependencies {
			if dependency.Domain == "runtime-image" && dependency.Identity != "" {
				protected = append(protected, dependency.Identity)
			}
		}
	}
	return protected, nil
}
