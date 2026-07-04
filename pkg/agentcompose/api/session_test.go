package api

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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

func (r *testSessionReconciler) ReconcileRuntimeState(_ context.Context, session *domain.Session) (*domain.Session, error) {
	r.calls++
	session.Summary.VMStatus = domain.VMStatusStopped
	return session, nil
}

func TestSessionHandlerGetAndListSessionsUseStoreAndReconciler(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sessionstore.NewWithConfig(&appconfig.Config{
		SessionRoot:          filepath.Join(root, "sessions"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "debian:bookworm-slim",
		GuestWorkspacePath:   "/workspace",
		JupyterProxyBasePath: "/agent-compose/session",
	})
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "api session", "", driverpkg.RuntimeDriverBoxlite, "debian:bookworm-slim", "", domain.SessionTypeManual, nil, nil, nil)
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

func (s errorSessionStore) GetSession(context.Context, string) (*domain.Session, error) {
	return nil, s.err
}

func (s errorSessionStore) ListSessions(context.Context, domain.SessionListOptions) (domain.SessionListResult, error) {
	return domain.SessionListResult{}, s.err
}
