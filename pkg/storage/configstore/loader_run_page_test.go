package configstore

import (
	"context"
	"testing"
	"time"

	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
)

func TestLoaderRunPageUsesStableCrossLoaderCursor(t *testing.T) {
	ctx := context.Background()
	store := FromDB(newMemoryDB(t))
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	for _, loaderID := range []string{"loader-a", "loader-b"} {
		if _, err := store.UpsertManagedLoader(ctx, domain.Loader{
			Summary: domain.LoaderSummary{
				ID:                 loaderID,
				Name:               loaderID,
				Runtime:            domain.LoaderRuntimeScheduler,
				ManagedProjectID:   "project-1",
				ManagedAgentName:   loaderID,
				ManagedSchedulerID: "scheduler-" + loaderID,
			},
			Script: "function main() {}",
		}); err != nil {
			t.Fatalf("upsert loader %s: %v", loaderID, err)
		}
	}
	newer := time.UnixMilli(1_720_000_000_500).UTC()
	older := newer.Add(-time.Second)
	for _, run := range []domain.LoaderRunSummary{
		{ID: "run-a", LoaderID: "loader-a", Status: domain.LoaderRunStatusSucceeded, StartedAt: newer},
		{ID: "run-b1", LoaderID: "loader-b", Status: domain.LoaderRunStatusSucceeded, StartedAt: newer},
		{ID: "run-b2", LoaderID: "loader-b", Status: domain.LoaderRunStatusSucceeded, StartedAt: newer},
		{ID: "run-old", LoaderID: "loader-b", Status: domain.LoaderRunStatusSucceeded, StartedAt: older},
	} {
		if err := store.CreateLoaderRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
	}

	first, err := store.ListLoaderRunsPage(ctx, loaders.LoaderRunPageFilter{
		LoaderIDs: []string{" loader-a ", "loader-b", "loader-a"},
		Limit:     2,
	})
	if err != nil || len(first) != 2 || first[0].ID != "run-b2" || first[1].ID != "run-b1" {
		t.Fatalf("first page=%#v err=%v", first, err)
	}
	second, err := store.ListLoaderRunsPage(ctx, loaders.LoaderRunPageFilter{
		LoaderIDs:       []string{"loader-a", "loader-b"},
		BeforeStartedAt: first[1].StartedAt,
		BeforeLoaderID:  first[1].LoaderID,
		BeforeRunID:     first[1].ID,
		Limit:           2,
	})
	if err != nil || len(second) != 2 || second[0].ID != "run-a" || second[1].ID != "run-old" {
		t.Fatalf("second page=%#v err=%v", second, err)
	}
	filtered, err := store.ListLoaderRunsPage(ctx, loaders.LoaderRunPageFilter{LoaderIDs: []string{"loader-a"}, Limit: 10})
	if err != nil || len(filtered) != 1 || filtered[0].ID != "run-a" {
		t.Fatalf("filtered page=%#v err=%v", filtered, err)
	}
	byID, err := store.GetLoaderRunForLoaders(ctx, []string{"loader-b"}, "run-old")
	if err != nil || byID.LoaderID != "loader-b" {
		t.Fatalf("GetLoaderRunForLoaders run=%#v err=%v", byID, err)
	}
	if _, err := store.GetLoaderRunForLoaders(ctx, []string{"loader-a"}, "run-old"); err == nil {
		t.Fatal("GetLoaderRunForLoaders accepted a run from another loader")
	}
	if _, err := store.GetLoaderRunForLoaders(ctx, nil, "missing"); err == nil {
		t.Fatal("GetLoaderRunForLoaders missing returned nil error")
	}
}
