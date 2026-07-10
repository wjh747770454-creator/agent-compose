package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"agent-compose/pkg/execution"
	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type KernelStore interface {
	GetSandbox(context.Context, string) (*domain.Sandbox, error)
	ListCells(context.Context, string) ([]domain.NotebookCell, error)
}

type CellExecutor interface {
	ExecuteCell(context.Context, *domain.Sandbox, string, string) (domain.NotebookCell, error)
	ExecuteCellStream(context.Context, *domain.Sandbox, string, string, execution.CellExecutionStream) (domain.NotebookCell, error)
}

type LoaderTopicPublisher interface {
	Publish(domain.LoaderTopicEvent) bool
}

type KernelHandler struct {
	store     KernelStore
	executor  CellExecutor
	publisher LoaderTopicPublisher
}

func NewKernelHandler(store KernelStore, executor CellExecutor, publisher LoaderTopicPublisher) *KernelHandler {
	return &KernelHandler{store: store, executor: executor, publisher: publisher}
}

func (h *KernelHandler) ExecuteCell(ctx context.Context, req *connect.Request[agentcomposev1.ExecuteCellRequest]) (*connect.Response[agentcomposev1.ExecuteCellResponse], error) {
	session, err := h.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if session.Summary.VMStatus != domain.VMStatusRunning {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("session is not running"))
	}
	cell, err := h.executor.ExecuteCell(ctx, session, CellTypeFromProto(req.Msg.GetType()), req.Msg.GetSource())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	loaded, err := h.store.GetSandbox(ctx, session.Summary.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	h.publishLoaderTopic("agent-compose.cell.completed", loaders.CellTopicPayload(session.Summary.ID, cell, "api"))
	return connect.NewResponse(&agentcomposev1.ExecuteCellResponse{Session: SessionSummaryToProto(&loaded.Summary), Cell: CellToProto(cell)}), nil
}

func (h *KernelHandler) ExecuteCellStream(ctx context.Context, req *connect.Request[agentcomposev1.ExecuteCellRequest], stream *connect.ServerStream[agentcomposev1.ExecuteCellStreamResponse]) error {
	PrepareStreamingHeaders(stream.ResponseHeader())
	session, err := h.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if session.Summary.VMStatus != domain.VMStatusRunning {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("session is not running"))
	}

	streamErr := func(sendErr error) error {
		if sendErr == nil {
			return nil
		}
		return connect.NewError(connect.CodeUnknown, sendErr)
	}
	cell, err := h.executor.ExecuteCellStream(ctx, session, CellTypeFromProto(req.Msg.GetType()), req.Msg.GetSource(), execution.CellExecutionStream{
		OnStart: func(cell domain.NotebookCell) error {
			return streamErr(stream.Send(&agentcomposev1.ExecuteCellStreamResponse{
				EventType: agentcomposev1.ExecuteCellStreamEventType_EXECUTE_CELL_STREAM_EVENT_TYPE_STARTED,
				Session:   SessionSummaryToProto(&session.Summary),
				Cell:      CellToProto(cell),
				CellId:    cell.ID,
			}))
		},
		OnChunk: func(cellID string, chunk domain.ExecChunk) error {
			if chunk.Text == "" {
				return nil
			}
			return streamErr(stream.Send(&agentcomposev1.ExecuteCellStreamResponse{
				EventType: agentcomposev1.ExecuteCellStreamEventType_EXECUTE_CELL_STREAM_EVENT_TYPE_OUTPUT,
				CellId:    cellID,
				Chunk:     chunk.Text,
				IsStderr:  domain.NormalizeStdioStream(chunk.Stream) == domain.StdioStderr,
			}))
		},
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	loaded, err := h.store.GetSandbox(ctx, session.Summary.ID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	h.publishLoaderTopic("agent-compose.cell.completed", loaders.CellTopicPayload(session.Summary.ID, cell, "api"))
	return streamErr(stream.Send(&agentcomposev1.ExecuteCellStreamResponse{
		EventType: agentcomposev1.ExecuteCellStreamEventType_EXECUTE_CELL_STREAM_EVENT_TYPE_COMPLETED,
		Session:   SessionSummaryToProto(&loaded.Summary),
		Cell:      CellToProto(cell),
		CellId:    cell.ID,
	}))
}

func (h *KernelHandler) ListCells(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.ListCellsResponse], error) {
	cells, err := h.store.ListCells(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &agentcomposev1.ListCellsResponse{SessionId: req.Msg.GetSessionId()}
	for _, cell := range cells {
		resp.Cells = append(resp.Cells, CellToProto(cell))
	}
	return connect.NewResponse(resp), nil
}

func (h *KernelHandler) publishLoaderTopic(topic string, payload map[string]any) {
	if h == nil || h.publisher == nil {
		return
	}
	h.publisher.Publish(domain.LoaderTopicEvent{Topic: topic, Payload: payload, CreatedAt: time.Now().UTC()})
}

func PrepareStreamingHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Set("Cache-Control", "no-cache, no-transform")
	headers.Set("X-Accel-Buffering", "no")
}
