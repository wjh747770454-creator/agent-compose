package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"agent-compose/pkg/dashboard"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

func prepareStreamingHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Set("X-Accel-Buffering", "no")
	headers.Set("Cache-Control", "no-cache")
}

type DashboardHandler struct {
	hub *dashboard.Hub
}

func NewDashboardHandler(hub *dashboard.Hub) *DashboardHandler {
	return &DashboardHandler{hub: hub}
}

func (h *DashboardHandler) GetDashboardOverview(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[agentcomposev1.DashboardOverviewResponse], error) {
	_ = req
	overview, err := h.hub.Current(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentcomposev1.DashboardOverviewResponse{Overview: overview}), nil
}

func (h *DashboardHandler) WatchDashboardOverview(ctx context.Context, req *connect.Request[emptypb.Empty], stream *connect.ServerStream[agentcomposev1.DashboardOverviewEvent]) error {
	_ = req
	prepareStreamingHeaders(stream.ResponseHeader())
	overview, err := h.hub.Current(ctx)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if err := stream.Send(&agentcomposev1.DashboardOverviewEvent{Overview: overview, Reason: "initial"}); err != nil {
		return connect.NewError(connect.CodeUnknown, err)
	}
	events, cancel := h.hub.Watch(ctx)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := stream.Send(&agentcomposev1.DashboardOverviewEvent{Overview: event.Overview, Reason: event.Reason}); err != nil {
				return connect.NewError(connect.CodeUnknown, err)
			}
		}
	}
}
