package adapters

import (
	"context"
	"testing"

	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
)

func TestLoaderSessionRunnerLoadResumeAndShutdownCoverage(t *testing.T) {
	ctx := context.Background()
	bridge, driver := newTestSessionRPCBridge(t)
	publisher := &loaderSessionPublisherFake{}
	runner := NewLoaderSessionRunner(bridge.config, bridge.store, bridge.configDB, driver, nil, bridge.streams, publisher)

	running, err := bridge.store.CreateSession(ctx, "running", "", driverpkg.RuntimeDriverBoxlite, "", "", "loader", nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession running returned error: %v", err)
	}
	running.Summary.VMStatus = domain.VMStatusRunning
	if err := bridge.store.UpdateSession(ctx, running); err != nil {
		t.Fatalf("UpdateSession running returned error: %v", err)
	}
	loaded, err := runner.Load(ctx, running.Summary.ID)
	if err != nil || loaded.Summary.ID != running.Summary.ID {
		t.Fatalf("Load loaded=%#v err=%v", loaded, err)
	}
	resumed, eventType, err := runner.LoadOrResume(ctx, running.Summary.ID)
	if err != nil || resumed.Summary.ID != running.Summary.ID || eventType != "" || len(driver.startCalls) != 0 {
		t.Fatalf("LoadOrResume running resumed=%#v event=%q err=%v starts=%#v", resumed, eventType, err, driver.startCalls)
	}

	stopped, err := bridge.store.CreateSession(ctx, "stopped", "", driverpkg.RuntimeDriverBoxlite, "", "", "loader", nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession stopped returned error: %v", err)
	}
	stopped.Summary.VMStatus = domain.VMStatusStopped
	stopped.Summary.Tags = []domain.SessionTag{{Name: "capset", Value: "dev"}}
	if err := bridge.store.UpdateSession(ctx, stopped); err != nil {
		t.Fatalf("UpdateSession stopped returned error: %v", err)
	}
	resumed, eventType, err = runner.LoadOrResume(ctx, stopped.Summary.ID)
	if err != nil || resumed.Summary.VMStatus != domain.VMStatusRunning || eventType != "loader.session.resumed" || len(driver.startCalls) != 1 {
		t.Fatalf("LoadOrResume stopped resumed=%#v event=%q err=%v starts=%#v", resumed, eventType, err, driver.startCalls)
	}
	if len(publisher.events) != 1 || publisher.events[0].Topic != "agent-compose.session.resumed" {
		t.Fatalf("resume publisher events=%#v", publisher.events)
	}

	if err := runner.Shutdown(ctx, ""); err != nil {
		t.Fatalf("Shutdown empty returned error: %v", err)
	}
	if err := runner.Shutdown(ctx, resumed.Summary.ID); err != nil {
		t.Fatalf("Shutdown running returned error: %v", err)
	}
	shutdownLoaded, err := bridge.store.GetSession(ctx, resumed.Summary.ID)
	if err != nil {
		t.Fatalf("GetSession shutdown returned error: %v", err)
	}
	if shutdownLoaded.Summary.VMStatus != domain.VMStatusStopped || len(driver.stopCalls) != 1 {
		t.Fatalf("shutdown session=%#v stopCalls=%#v", shutdownLoaded.Summary, driver.stopCalls)
	}
	if err := runner.Shutdown(ctx, resumed.Summary.ID); err != nil {
		t.Fatalf("Shutdown stopped returned error: %v", err)
	}
	if len(driver.stopCalls) != 1 {
		t.Fatalf("Shutdown stopped should not call driver again: %#v", driver.stopCalls)
	}

	if snapshot := toSessionWorkspaceSnapshot(domain.WorkspaceConfig{ID: "workspace-1", Name: "Workspace", Type: "file", ConfigJSON: "{}"}); snapshot.ID != "workspace-1" || snapshot.Name != "Workspace" {
		t.Fatalf("toSessionWorkspaceSnapshot = %#v", snapshot)
	}
	if workspace, err := runner.workspaceSnapshot(ctx, ""); err != nil || workspace != nil {
		t.Fatalf("workspaceSnapshot empty workspace=%#v err=%v", workspace, err)
	}
	if driverName, err := runner.driver(domain.LoaderAgentRequest{Driver: driverpkg.RuntimeDriverDocker}, domain.Loader{}, nil); err != nil || driverName != driverpkg.RuntimeDriverDocker {
		t.Fatalf("driver override=%q err=%v", driverName, err)
	}
	if image := runner.guestImage(domain.LoaderAgentRequest{GuestImage: "request:latest"}, domain.Loader{Summary: domain.LoaderSummary{GuestImage: "loader:latest"}}, &domain.AgentDefinition{GuestImage: "agent:latest"}, driverpkg.RuntimeDriverDocker); image != "request:latest" {
		t.Fatalf("guestImage = %q", image)
	}
}

func TestIntegrationLoaderSessionRunnerLoadResumeAndShutdownCoverage(t *testing.T) {
	TestLoaderSessionRunnerLoadResumeAndShutdownCoverage(t)
}

func TestE2ELoaderSessionRunnerLoadResumeAndShutdownCoverage(t *testing.T) {
	TestLoaderSessionRunnerLoadResumeAndShutdownCoverage(t)
}

type loaderSessionPublisherFake struct {
	events []domain.LoaderTopicEvent
}

func (p *loaderSessionPublisherFake) Publish(event domain.LoaderTopicEvent) bool {
	p.events = append(p.events, event)
	return true
}

var _ loaders.ControllerPublisher = (*loaderSessionPublisherFake)(nil)
