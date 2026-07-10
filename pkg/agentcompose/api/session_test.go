package api

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sessionstore"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type testSessionReconciler struct {
	calls int
}

func (r *testSessionReconciler) ReconcileRuntimeState(_ context.Context, session *domain.Sandbox) (*domain.Sandbox, error) {
	r.calls++
	session.Summary.VMStatus = domain.VMStatusStopped
	return session, nil
}

func TestSessionHandlerGetAndListSessionsUseStoreAndReconciler(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sessionstore.NewWithConfig(&appconfig.Config{
		SandboxRoot:          filepath.Join(root, "sandboxes"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "debian:bookworm-slim",
		GuestWorkspacePath:   "/workspace",
		JupyterProxyBasePath: "/agent-compose/session",
	})
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSandbox(ctx, "api session", "", driverpkg.RuntimeDriverBoxlite, "debian:bookworm-slim", "", domain.SandboxTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	reconciler := &testSessionReconciler{}
	handler := &SessionHandler{store: store, reconciler: reconciler}

	got, err := handler.GetSession(ctx, connect.NewRequest(&agentcomposev1.SessionIDRequest{SessionId: session.Summary.ID}))
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if got.Msg.GetSession().GetSummary().GetSessionId() != session.Summary.ID {
		t.Fatalf("GetSession id = %q, want %q", got.Msg.GetSession().GetSummary().GetSessionId(), session.Summary.ID)
	}
	if got.Msg.GetSession().GetSummary().GetVmStatus() != domain.VMStatusStopped {
		t.Fatalf("GetSession status = %q, want reconciled stopped", got.Msg.GetSession().GetSummary().GetVmStatus())
	}

	listed, err := handler.ListSessions(ctx, connect.NewRequest(&agentcomposev1.ListSessionsRequest{Limit: 10}))
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if len(listed.Msg.GetSessions()) != 1 {
		t.Fatalf("ListSessions count = %d, want 1", len(listed.Msg.GetSessions()))
	}
	if listed.Msg.GetSessions()[0].GetVmStatus() != domain.VMStatusStopped {
		t.Fatalf("ListSessions status = %q, want reconciled stopped", listed.Msg.GetSessions()[0].GetVmStatus())
	}
	if reconciler.calls != 2 {
		t.Fatalf("reconciler calls = %d, want 2", reconciler.calls)
	}
}

func TestV1CompatibilityMappingPreservesSessionWireNames(t *testing.T) {
	now := time.Date(2026, 7, 9, 10, 11, 12, 0, time.UTC)
	session := &domain.Sandbox{
		Summary: domain.SandboxSummary{
			ID:        "sandbox-compatible-id",
			Title:     "v1 compatibility",
			VMStatus:  domain.VMStatusRunning,
			Driver:    driverpkg.RuntimeDriverDocker,
			Tags:      []domain.SandboxTag{{Name: "project", Value: "demo"}},
			CreatedAt: now,
			UpdatedAt: now,
		},
		EnvItems: []domain.SandboxEnvVar{{Name: "PLAIN", Value: "visible"}, {Name: "SECRET", Value: "hidden", Secret: true}},
	}

	detail := SessionDetailToProto(session)
	if detail.GetSummary().GetSessionId() != session.Summary.ID || detail.GetSummary().GetTitle() != session.Summary.Title {
		t.Fatalf("v1 session summary mapping = %#v", detail.GetSummary())
	}
	if got := detail.GetEnvItems()[1].GetValue(); got != secretRedactedValue {
		t.Fatalf("v1 secret env value = %q, want redacted", got)
	}

	cell := domain.NotebookCell{
		ID:            "cell-1",
		Type:          "agent",
		Source:        "prompt",
		Agent:         "codex",
		AgentThreadID: "provider-thread-compatible-id",
		Success:       true,
		CreatedAt:     now,
	}
	if got := CellToProto(cell).GetAgentSessionId(); got != cell.AgentThreadID {
		t.Fatalf("v1 cell agent_thread_id = %q, want %q", got, cell.AgentThreadID)
	}
	if got := AgentRunToProto(cell).GetAgentSessionId(); got != cell.AgentThreadID {
		t.Fatalf("v1 agent run agent_thread_id = %q, want %q", got, cell.AgentThreadID)
	}
}

func TestV1CompatibilityHandlersMapSessionIDToSandboxStore(t *testing.T) {
	ctx := context.Background()
	store := &apiHandlerSessionStore{
		session: &domain.Sandbox{Summary: domain.SandboxSummary{ID: "v1-session-wire", VMStatus: domain.VMStatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now()}},
		cells:   []domain.NotebookCell{{ID: "cell-1", CreatedAt: time.Now()}},
		events:  []domain.SandboxEvent{{ID: "event-1", CreatedAt: time.Now()}},
	}

	sessionHandler := &SessionHandler{store: store}
	if resp, err := sessionHandler.GetSession(ctx, connect.NewRequest(&agentcomposev1.SessionIDRequest{SessionId: "v1-session-wire"})); err != nil || resp.Msg.GetSession().GetSummary().GetSessionId() != "v1-session-wire" {
		t.Fatalf("GetSession compatibility resp=%#v err=%v", resp, err)
	}

	kernelHandler := NewKernelHandler(store, nil, nil)
	if resp, err := kernelHandler.ListCells(ctx, connect.NewRequest(&agentcomposev1.SessionIDRequest{SessionId: "v1-session-wire"})); err != nil || resp.Msg.GetSessionId() != "v1-session-wire" {
		t.Fatalf("ListCells compatibility resp=%#v err=%v", resp, err)
	}

	agentHandler := NewAgentHandler(store, nil, nil, nil)
	if resp, err := agentHandler.ListSessionEvents(ctx, connect.NewRequest(&agentcomposev1.SessionIDRequest{SessionId: "v1-session-wire"})); err != nil || resp.Msg.GetSessionId() != "v1-session-wire" {
		t.Fatalf("ListSessionEvents compatibility resp=%#v err=%v", resp, err)
	}

	if want := []string{"v1-session-wire"}; !reflect.DeepEqual(store.getSandboxIDs, want) {
		t.Fatalf("v1 GetSession store ids = %#v, want %#v", store.getSandboxIDs, want)
	}
	if want := []string{"v1-session-wire"}; !reflect.DeepEqual(store.listCellsIDs, want) {
		t.Fatalf("v1 ListCells store ids = %#v, want %#v", store.listCellsIDs, want)
	}
	if want := []string{"v1-session-wire"}; !reflect.DeepEqual(store.listEventsIDs, want) {
		t.Fatalf("v1 ListSessionEvents store ids = %#v, want %#v", store.listEventsIDs, want)
	}
}

func TestSessionHandlerGetSessionNotFoundErrorCompatibility(t *testing.T) {
	tests := []error{
		fmt.Errorf("load session: %w", os.ErrNotExist),
		fmt.Errorf("load session: %w", sql.ErrNoRows),
		fmt.Errorf("load session: %w", domain.ErrNotFound),
	}
	for _, storeErr := range tests {
		handler := &SessionHandler{store: errorSessionStore{err: storeErr}}
		_, err := handler.GetSession(context.Background(), connect.NewRequest(&agentcomposev1.SessionIDRequest{SessionId: "missing"}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("GetSession error code = %v, want %v for %v", connect.CodeOf(err), connect.CodeNotFound, storeErr)
		}
	}

	handler := &SessionHandler{store: errorSessionStore{err: fmt.Errorf("load session: %w", os.ErrPermission)}}
	_, err := handler.GetSession(context.Background(), connect.NewRequest(&agentcomposev1.SessionIDRequest{SessionId: "missing"}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("GetSession internal error code = %v, want %v", connect.CodeOf(err), connect.CodeInternal)
	}
}

type errorSessionStore struct {
	err error
}

func (s errorSessionStore) GetSandbox(context.Context, string) (*domain.Sandbox, error) {
	return nil, s.err
}

func (s errorSessionStore) ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error) {
	return domain.SandboxListResult{}, s.err
}
