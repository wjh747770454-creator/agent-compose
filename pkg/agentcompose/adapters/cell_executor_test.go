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

type fakeCellRuntime struct {
	result domain.ExecResult
}

func (r fakeCellRuntime) EnsureSession(context.Context, *domain.Session, domain.VMState, domain.ProxyState) (domain.SessionVMInfo, error) {
	return domain.SessionVMInfo{}, nil
}

func (r fakeCellRuntime) StopSession(context.Context, *domain.Session, domain.VMState) (bool, error) {
	return false, nil
}

func (r fakeCellRuntime) Exec(context.Context, *domain.Session, domain.VMState, domain.ExecSpec) (domain.ExecResult, error) {
	return r.result, nil
}

func (r fakeCellRuntime) ExecStream(_ context.Context, _ *domain.Session, _ domain.VMState, _ domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	if stream != nil {
		stream(domain.ExecChunk{Text: r.result.Stdout})
	}
	return r.result, nil
}

func TestCellExecutorExecuteCellPersistsCellAndEvent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:             root,
		SessionRoot:          filepath.Join(root, "sessions"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "guest:latest",
		GuestWorkspacePath:   "/workspace",
		GuestStateRoot:       "/state",
		JupyterProxyBasePath: "/agent-compose/session",
		SessionStartTimeout:  2 * time.Second,
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "cell session", "", driverpkg.RuntimeDriverBoxlite, "guest:latest", "", domain.SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	executor := NewCellExecutor(config, store, fakeRuntimeProvider{runtime: fakeCellRuntime{result: domain.ExecResult{
		Stdout:   "hello\n",
		Output:   "hello\n",
		ExitCode: 0,
		Success:  true,
	}}}, nil)

	cell, err := executor.ExecuteCell(ctx, session, execution.CellTypeShell, "echo hello")
	if err != nil {
		t.Fatalf("ExecuteCell returned error: %v", err)
	}
	if !cell.Success || cell.Stdout != "hello\n" {
		t.Fatalf("cell = %#v", cell)
	}
	cells, err := store.ListCells(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListCells returned error: %v", err)
	}
	if len(cells) != 1 || cells[0].ID != cell.ID {
		t.Fatalf("stored cells = %#v", cells)
	}
	events, err := store.ListEvents(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != "kernel.cell.succeeded" {
		t.Fatalf("events = %#v", events)
	}
}
