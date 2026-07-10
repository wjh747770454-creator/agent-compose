package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sessions"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type SessionDelegate interface {
	CreateSession(context.Context, *connect.Request[agentcomposev1.CreateSessionRequest]) (*connect.Response[agentcomposev1.SessionResponse], error)
	ResumeSession(context.Context, *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error)
	StopSession(context.Context, *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error)
	GetSessionProxy(context.Context, *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionProxyResponse], error)
}

type WatchSessionStore interface {
	GetSandbox(context.Context, string) (*domain.Sandbox, error)
	ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error)
}

type SessionRuntimeReconciler interface {
	ReconcileRuntimeState(context.Context, *domain.Sandbox) (*domain.Sandbox, error)
}

type SessionHandler struct {
	delegate   SessionDelegate
	store      WatchSessionStore
	streams    *sessions.StreamBroker
	reconciler SessionRuntimeReconciler
}

func NewSessionHandler(delegate SessionDelegate, store WatchSessionStore, streams *sessions.StreamBroker) *SessionHandler {
	handler := &SessionHandler{delegate: delegate, store: store, streams: streams}
	if reconciler, ok := delegate.(SessionRuntimeReconciler); ok {
		handler.reconciler = reconciler
	}
	return handler
}

func SessionListOptionsFromProto(req *agentcomposev1.ListSessionsRequest) (domain.SandboxListOptions, error) {
	if req == nil {
		return domain.SandboxListOptions{}, nil
	}
	createdFrom, err := ParseOptionalRFC3339(req.GetCreatedFrom(), "created_from")
	if err != nil {
		return domain.SandboxListOptions{}, err
	}
	createdTo, err := ParseOptionalRFC3339(req.GetCreatedTo(), "created_to")
	if err != nil {
		return domain.SandboxListOptions{}, err
	}
	updatedFrom, err := ParseOptionalRFC3339(req.GetUpdatedFrom(), "updated_from")
	if err != nil {
		return domain.SandboxListOptions{}, err
	}
	updatedTo, err := ParseOptionalRFC3339(req.GetUpdatedTo(), "updated_to")
	if err != nil {
		return domain.SandboxListOptions{}, err
	}
	return domain.SandboxListOptions{
		SandboxType:        req.GetSessionType(),
		TriggerSourceQuery: req.GetTriggerSourceQuery(),
		TitleQuery:         req.GetTitleQuery(),
		WorkspaceQuery:     req.GetWorkspaceQuery(),
		Driver:             req.GetDriver(),
		VMStatus:           req.GetVmStatus(),
		CreatedFrom:        createdFrom,
		CreatedTo:          createdTo,
		UpdatedFrom:        updatedFrom,
		UpdatedTo:          updatedTo,
		Offset:             int(req.GetOffset()),
		Limit:              int(req.GetLimit()),
	}, nil
}

func ParseOptionalRFC3339(raw, field string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	return parsed, nil
}

func (h *SessionHandler) CreateSession(ctx context.Context, req *connect.Request[agentcomposev1.CreateSessionRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	return h.delegate.CreateSession(ctx, req)
}

func (h *SessionHandler) ResumeSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	return h.delegate.ResumeSession(ctx, req)
}

func (h *SessionHandler) StopSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	return h.delegate.StopSession(ctx, req)
}

func (h *SessionHandler) GetSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	session, err := h.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		if isSessionNotFoundError(err) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	session = h.reconcileRuntimeState(ctx, session, "get")
	return connect.NewResponse(&agentcomposev1.SessionResponse{Session: SessionDetailToProto(session)}), nil
}

func isSessionNotFoundError(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, domain.ErrNotFound)
}

func (h *SessionHandler) ListSessions(ctx context.Context, req *connect.Request[agentcomposev1.ListSessionsRequest]) (*connect.Response[agentcomposev1.ListSessionsResponse], error) {
	options, err := SessionListOptionsFromProto(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	result, err := h.store.ListSandboxes(ctx, options)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &agentcomposev1.ListSessionsResponse{
		TotalCount: uint32(result.TotalCount),
		HasMore:    result.HasMore,
		NextOffset: uint32(result.NextOffset),
	}
	for _, session := range result.Sandboxes {
		session = h.reconcileRuntimeState(ctx, session, "list")
		resp.Sessions = append(resp.Sessions, SessionSummaryToProto(&session.Summary))
	}
	return connect.NewResponse(resp), nil
}

func (h *SessionHandler) GetSessionProxy(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionProxyResponse], error) {
	return h.delegate.GetSessionProxy(ctx, req)
}

func (h *SessionHandler) WatchSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest], stream *connect.ServerStream[agentcomposev1.WatchSessionResponse]) error {
	PrepareStreamingHeaders(stream.ResponseHeader())
	session, err := h.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if sendErr := stream.Send(&agentcomposev1.WatchSessionResponse{
		EventType: agentcomposev1.WatchSessionEventType_WATCH_SESSION_EVENT_TYPE_SESSION_UPDATED,
		Session:   SessionSummaryToProto(&session.Summary),
	}); sendErr != nil {
		return connect.NewError(connect.CodeUnknown, sendErr)
	}
	events, cancel := h.streams.Subscribe(session.Summary.ID)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if sendErr := stream.Send(WatchSessionResponseToProto(event)); sendErr != nil {
				return connect.NewError(connect.CodeUnknown, sendErr)
			}
		}
	}
}

func (h *SessionHandler) reconcileRuntimeState(ctx context.Context, session *domain.Sandbox, operation string) *domain.Sandbox {
	if h.reconciler == nil {
		return session
	}
	reconciled, err := h.reconciler.ReconcileRuntimeState(ctx, session)
	if err != nil {
		slog.Warn("failed to reconcile sandbox runtime state during "+operation, "sandbox_id", session.Summary.ID, "error", err)
		return session
	}
	return reconciled
}
