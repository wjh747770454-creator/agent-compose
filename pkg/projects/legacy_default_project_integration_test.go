package projects_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samber/do/v2"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/images"
	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/projects"
	"agent-compose/pkg/storage/configstore"
)

func TestIntegrationLegacyDefaultProjectAdoptsLoaderHistoryAtCurrentRevision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:           root,
		DbAddr:             filepath.Join(root, "data.db"),
		RuntimeDriver:      driverpkg.RuntimeDriverDocker,
		DockerDefaultImage: "guest:latest",
	}
	di := do.New()
	do.ProvideValue(di, config)
	store, err := configstore.NewConfigStore(di)
	if err != nil {
		t.Fatalf("create config store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.DB().Close(); err != nil {
			t.Errorf("close config store: %v", err)
		}
	})

	agent, err := store.CreateAgentDefinition(ctx, domain.AgentDefinition{
		ID:         "legacy-agent-source",
		Name:       "ai资讯推送",
		Enabled:    true,
		Provider:   "codex",
		Driver:     driverpkg.RuntimeDriverDocker,
		GuestImage: "guest:latest",
	})
	if err != nil {
		t.Fatalf("create legacy agent: %v", err)
	}
	const script = `
scheduler.interval("hourly", function hourly() {}, 3600000);
scheduler.on("news.ready", "on-news", function onNews() {});
`
	loader, err := store.CreateLoader(ctx, domain.Loader{
		Summary: domain.LoaderSummary{
			ID:            "legacy-loader-source",
			Name:          "资讯推送任务",
			Enabled:       false,
			Runtime:       domain.LoaderRuntimeScheduler,
			AgentID:       agent.ID,
			Driver:        driverpkg.RuntimeDriverDocker,
			GuestImage:    "guest:latest",
			DefaultAgent:  "codex",
			SandboxPolicy: domain.LoaderSandboxPolicySticky,
		},
		Script: script,
	})
	if err != nil {
		t.Fatalf("create legacy loader: %v", err)
	}
	engine := &loaders.QJSLoaderEngine{}
	validation, err := engine.Validate(ctx, loader.Summary.Runtime, loader.Script)
	if err != nil {
		t.Fatalf("validate legacy loader: %v", err)
	}
	if _, err := store.ReplaceLoaderTriggers(ctx, loader.Summary.ID, validation.Triggers); err != nil {
		t.Fatalf("create legacy triggers: %v", err)
	}
	if err := store.SetLoaderTriggerEnabled(ctx, loader.Summary.ID, "on-news", false); err != nil {
		t.Fatalf("disable legacy trigger: %v", err)
	}
	startedAt := time.Now().UTC().Add(-time.Hour)
	if err := store.CreateLoaderRun(ctx, domain.LoaderRunSummary{
		ID: "legacy-run", LoaderID: loader.Summary.ID, TriggerID: "hourly", TriggerKind: domain.LoaderTriggerKindInterval,
		Status: domain.LoaderRunStatusSucceeded, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("create legacy run: %v", err)
	}
	if err := store.AddLoaderEvent(ctx, domain.LoaderEvent{
		ID: "legacy-event", LoaderID: loader.Summary.ID, RunID: "legacy-run", Type: "loader.completed", CreatedAt: startedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("create legacy event: %v", err)
	}

	controller := projects.NewController(projects.ControllerDependencies{
		Config:  config,
		Store:   store,
		Images:  legacyProjectImageBackend{},
		Loaders: legacyProjectLoaderValidator{engine: engine},
		Volumes: legacyProjectVolumeManager{},
	})
	result, err := controller.SyncLegacyDefaultProject(ctx)
	if err != nil {
		t.Fatalf("SyncLegacyDefaultProject returned error: %v", err)
	}
	if !result.Applied || len(result.Issues) != 0 {
		t.Fatalf("sync result = %#v", result)
	}
	if len(result.Agents) != 1 || !strings.HasPrefix(result.Agents[0].AgentName, "legacy-agent-") {
		t.Fatalf("projected Chinese agents = %#v", result.Agents)
	}

	projectID, err := domain.StableProjectID(projects.LegacyDefaultProjectName, "")
	if err != nil {
		t.Fatalf("stable project id: %v", err)
	}
	project, err := store.GetProject(ctx, projectID)
	if err != nil {
		t.Fatalf("load legacy project: %v", err)
	}
	page, err := store.ListProjectSchedulersPage(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("list project schedulers page: %v", err)
	}
	if len(page) != 1 || page[0].Revision != project.CurrentRevision || page[0].ManagedLoaderID != loader.Summary.ID || page[0].Enabled || page[0].RunCount != 1 {
		t.Fatalf("project scheduler page = %#v, project = %#v", page, project)
	}

	adopted, err := store.GetLoader(ctx, loader.Summary.ID)
	if err != nil {
		t.Fatalf("load adopted loader: %v", err)
	}
	if adopted.Summary.ManagedProjectID != project.ID || adopted.Summary.ManagedRevision != project.CurrentRevision || adopted.Summary.ManagedAgentName != page[0].AgentName || adopted.Summary.Enabled {
		t.Fatalf("adopted loader = %#v", adopted.Summary)
	}
	triggerEnabled := make(map[string]bool, len(adopted.Triggers))
	for _, trigger := range adopted.Triggers {
		triggerEnabled[trigger.ID] = trigger.Enabled
	}
	if len(triggerEnabled) != 2 || !triggerEnabled["hourly"] || triggerEnabled["on-news"] {
		t.Fatalf("adopted trigger states = %#v", triggerEnabled)
	}
	if runs, err := store.ListLoaderRuns(ctx, loader.Summary.ID, 10); err != nil || len(runs) != 1 || runs[0].ID != "legacy-run" {
		t.Fatalf("adopted loader runs = %#v, err = %v", runs, err)
	}
	if events, err := store.ListLoaderEvents(ctx, loader.Summary.ID, 10); err != nil || len(events) != 1 || events[0].ID != "legacy-event" {
		t.Fatalf("adopted loader events = %#v, err = %v", events, err)
	}

	repeated, err := controller.SyncLegacyDefaultProject(ctx)
	if err != nil || !repeated.Applied || repeated.Revision.Revision != project.CurrentRevision {
		t.Fatalf("repeated sync = %#v, err = %v", repeated, err)
	}
	page, err = store.ListProjectSchedulersPage(ctx, "", "", 10)
	if err != nil || len(page) != 1 || page[0].ManagedLoaderID != loader.Summary.ID || page[0].RunCount != 1 {
		t.Fatalf("scheduler page after repeated sync = %#v, err = %v", page, err)
	}
}

type legacyProjectLoaderValidator struct {
	engine *loaders.QJSLoaderEngine
}

func (v legacyProjectLoaderValidator) Validate(ctx context.Context, runtime, script string) (loaders.LoaderValidationResult, error) {
	return v.engine.Validate(ctx, runtime, script)
}

func (legacyProjectLoaderValidator) Refresh(context.Context) error { return nil }

type legacyProjectImageBackend struct{}

func (legacyProjectImageBackend) ListImages(context.Context, images.ListRequest) (images.ListResult, error) {
	return images.ListResult{}, nil
}

func (legacyProjectImageBackend) PullImage(context.Context, images.PullRequest) (images.PullResult, error) {
	return images.PullResult{}, nil
}

func (legacyProjectImageBackend) InspectImage(context.Context, images.InspectRequest) (images.InspectResult, error) {
	return images.InspectResult{}, nil
}

func (legacyProjectImageBackend) RemoveImage(context.Context, images.RemoveRequest) (images.RemoveResult, error) {
	return images.RemoveResult{}, nil
}

type legacyProjectVolumeManager struct{}

func (legacyProjectVolumeManager) Ensure(context.Context, domain.VolumeRecord) (domain.VolumeRecord, bool, error) {
	return domain.VolumeRecord{}, false, nil
}

func (legacyProjectVolumeManager) Inspect(context.Context, string) (domain.VolumeRecord, error) {
	return domain.VolumeRecord{}, nil
}

func (legacyProjectVolumeManager) ReplaceProjectVolumes(context.Context, string, map[string]domain.ProjectVolumeLink) error {
	return nil
}

func (legacyProjectVolumeManager) RemoveProjectVolumes(context.Context, string) error { return nil }
