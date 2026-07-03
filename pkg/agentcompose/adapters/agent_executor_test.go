package adapters

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sessionstore"
)

func TestAgentExecutorExecuteAgentRequestPersistsCellAndEvents(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:             root,
		SessionRoot:          filepath.Join(root, "sessions"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "guest:latest",
		GuestWorkspacePath:   "/workspace",
		GuestStateRoot:       "/data/state",
		GuestHomePath:        "/root",
		JupyterProxyBasePath: "/agent-compose/session",
		SessionStartTimeout:  2 * time.Second,
		AgentTimeout:         2 * time.Second,
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "agent executor session", "", driverpkg.RuntimeDriverBoxlite, "guest:latest", "", domain.SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	runtime := &fakeAgentRuntime{}
	runner := NewAgentRunner(config, store, nil, nil, fakeRuntimeProvider{runtime: runtime})
	executor := NewAgentExecutor(config, store, nil, runner)

	cell, userEvent, assistantEvent, err := executor.ExecuteAgentRequest(ctx, session, execution.ExecuteAgentRequest{
		Agent:   "codex",
		Message: "hello",
	})
	if err != nil {
		t.Fatalf("ExecuteAgentRequest returned error: %v", err)
	}
	if !cell.Success || cell.Type != execution.CellTypeAgent || cell.AgentSessionID != "agent-session-1" {
		t.Fatalf("cell = %#v", cell)
	}
	if userEvent.Type != "agent.user" || assistantEvent.Type != "agent.assistant" {
		t.Fatalf("events = %#v %#v", userEvent, assistantEvent)
	}
	cells, err := store.ListCells(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListCells returned error: %v", err)
	}
	if len(cells) == 0 || cells[len(cells)-1].ID != cell.ID || !cells[len(cells)-1].Success {
		t.Fatalf("stored cells = %#v", cells)
	}
	events, err := store.ListEvents(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListEvents returned error: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events = %#v, want user and assistant events", events)
	}
}
