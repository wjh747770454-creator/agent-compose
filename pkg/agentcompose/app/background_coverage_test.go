package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sessionstore"
)

func TestReconcilePendingSessionStateMarksStaleStartupFailed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{DataRoot: root, SessionRoot: filepath.Join(root, "sessions"), RuntimeDriver: driverpkg.RuntimeDriverBoxlite}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "stale", "", driverpkg.RuntimeDriverBoxlite, "", "", "", nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusPending
	session.Summary.CreatedAt = time.Now().Add(-time.Hour)
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	if err := store.SaveVMState(session.Summary.ID, domain.VMState{Driver: driverpkg.RuntimeDriverBoxlite}); err != nil {
		t.Fatalf("SaveVMState returned error: %v", err)
	}
	reconciled, err := reconcilePendingSessionState(ctx, store, session, time.Now())
	if err != nil {
		t.Fatalf("reconcilePendingSessionState returned error: %v", err)
	}
	if reconciled.Summary.VMStatus != domain.VMStatusFailed {
		t.Fatalf("status = %q", reconciled.Summary.VMStatus)
	}
	vmState, err := store.GetVMState(session.Summary.ID)
	if err != nil {
		t.Fatalf("GetVMState returned error: %v", err)
	}
	if vmState.LastError != stalePendingSessionLastError || vmState.StoppedAt.IsZero() {
		t.Fatalf("vmState = %#v", vmState)
	}
	events, err := store.ListEvents(ctx, session.Summary.ID)
	if err != nil || len(events) != 1 || events[0].Type != "session.startup_interrupted" {
		t.Fatalf("events=%#v err=%v", events, err)
	}

	running := &domain.Session{Summary: domain.SessionSummary{VMStatus: domain.VMStatusRunning}}
	if got, err := reconcilePendingSessionState(ctx, store, running, time.Now()); err != nil || got != running {
		t.Fatalf("running session got=%#v err=%v", got, err)
	}
	fresh := &domain.Session{Summary: domain.SessionSummary{VMStatus: domain.VMStatusPending, CreatedAt: time.Now().Add(time.Hour)}}
	if got, err := reconcilePendingSessionState(ctx, store, fresh, time.Now()); err != nil || got != fresh {
		t.Fatalf("fresh session got=%#v err=%v", got, err)
	}
	if err := startCapabilityProxy(context.Background(), nil); err != nil {
		t.Fatalf("startCapabilityProxy nil returned error: %v", err)
	}
}

func TestIntegrationReconcilePendingSessionStateMarksStaleStartupFailed(t *testing.T) {
	TestReconcilePendingSessionStateMarksStaleStartupFailed(t)
}

func TestE2EReconcilePendingSessionStateMarksStaleStartupFailed(t *testing.T) {
	TestReconcilePendingSessionStateMarksStaleStartupFailed(t)
}
