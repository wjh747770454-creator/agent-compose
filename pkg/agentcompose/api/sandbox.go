package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type SandboxStore interface {
	GetSession(context.Context, string) (*domain.Session, error)
	RemoveSession(context.Context, string) error
}

type SandboxDashboardNotifier interface {
	Notify(string)
}

type SandboxHandler struct {
	delegate   SessionDelegate
	store      SandboxStore
	reconciler SessionRuntimeReconciler
	dashboard  SandboxDashboardNotifier
}

func NewSandboxHandler(delegate SessionDelegate, store SandboxStore, dashboard SandboxDashboardNotifier) *SandboxHandler {
	handler := &SandboxHandler{delegate: delegate, store: store, dashboard: dashboard}
	if reconciler, ok := delegate.(SessionRuntimeReconciler); ok {
		handler.reconciler = reconciler
	}
	return handler
}

func (h *SandboxHandler) RemoveSandbox(ctx context.Context, req *connect.Request[agentcomposev2.RemoveSandboxRequest]) (*connect.Response[agentcomposev2.RemoveSandboxResponse], error) {
	sandboxID := strings.TrimSpace(req.Msg.GetSandboxId())
	if sandboxID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("sandbox id is required"))
	}
	if sandboxID == "." || sandboxID == ".." || filepath.Base(sandboxID) != sandboxID {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid sandbox id %q", sandboxID))
	}
	session, err := h.store.GetSession(ctx, sandboxID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if h.reconciler != nil {
		reconciled, recErr := h.reconciler.ReconcileRuntimeState(ctx, session)
		if recErr != nil {
			slog.Warn("failed to reconcile sandbox runtime state before remove", "sandbox_id", sandboxID, "error", recErr)
			return nil, connect.NewError(connect.CodeInternal, recErr)
		}
		session = reconciled
	}
	stopped := false
	if session.Summary.VMStatus == domain.VMStatusRunning {
		if !req.Msg.GetForce() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("sandbox %s is running", sandboxID))
		}
		if _, err := h.delegate.StopSession(ctx, connect.NewRequest(&agentcomposev1.SessionIDRequest{SessionId: sandboxID})); err != nil {
			return nil, err
		}
		stopped = true
	}
	if err := h.store.RemoveSession(ctx, sandboxID); err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if h.dashboard != nil {
		h.dashboard.Notify("session_removed")
	}
	return connect.NewResponse(&agentcomposev2.RemoveSandboxResponse{
		SandboxId: sandboxID,
		Stopped:   stopped,
		Removed:   true,
	}), nil
}
