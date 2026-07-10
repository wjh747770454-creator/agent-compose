package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/runs"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type RunAgentDelegate interface {
	RunAgent(context.Context, *connect.Request[agentcomposev2.RunAgentRequest]) (*connect.Response[agentcomposev2.RunAgentResponse], error)
	StartRun(context.Context, *connect.Request[agentcomposev2.StartRunRequest]) (*connect.Response[agentcomposev2.StartRunResponse], error)
	RunAgentStream(context.Context, *connect.Request[agentcomposev2.RunAgentRequest], *connect.ServerStream[agentcomposev2.RunAgentStreamResponse]) error
	RunAttach(context.Context, *connect.BidiStream[agentcomposev2.RunAttachRequest, agentcomposev2.RunAttachResponse]) error
}

type ActiveRunStopper interface {
	StopActiveRun(context.Context, string, string) (bool, error)
}

type RunStore interface {
	runs.Store
	ListProjectRunsByOptions(context.Context, domain.ProjectRunListOptions) ([]domain.ProjectRunRecord, error)
}

type RunHandler struct {
	delegate RunAgentDelegate
	stopper  ActiveRunStopper
	store    RunStore
	runLogs  *runs.RunLogHub
}

func NewRunHandler(delegate RunAgentDelegate, store RunStore, stoppers ...ActiveRunStopper) *RunHandler {
	handler := &RunHandler{delegate: delegate, store: store}
	if len(stoppers) > 0 {
		handler.stopper = stoppers[0]
	}
	return handler
}

func NewRunHandlerWithRunLogHub(delegate RunAgentDelegate, store RunStore, hub *runs.RunLogHub, stoppers ...ActiveRunStopper) *RunHandler {
	handler := NewRunHandler(delegate, store, stoppers...)
	handler.runLogs = hub
	return handler
}

func (h *RunHandler) RunAgent(ctx context.Context, req *connect.Request[agentcomposev2.RunAgentRequest]) (*connect.Response[agentcomposev2.RunAgentResponse], error) {
	return h.delegate.RunAgent(ctx, req)
}

func (h *RunHandler) StartRun(ctx context.Context, req *connect.Request[agentcomposev2.StartRunRequest]) (*connect.Response[agentcomposev2.StartRunResponse], error) {
	if h.delegate == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("start run is not configured"))
	}
	return h.delegate.StartRun(ctx, req)
}

func (h *RunHandler) RunAgentStream(ctx context.Context, req *connect.Request[agentcomposev2.RunAgentRequest], stream *connect.ServerStream[agentcomposev2.RunAgentStreamResponse]) error {
	return h.delegate.RunAgentStream(ctx, req, stream)
}

func (h *RunHandler) RunAttach(ctx context.Context, stream *connect.BidiStream[agentcomposev2.RunAttachRequest, agentcomposev2.RunAttachResponse]) error {
	if h.delegate == nil {
		return connect.NewError(connect.CodeUnimplemented, fmt.Errorf("run attach is not configured"))
	}
	if err := h.delegate.RunAttach(ctx, stream); err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return connectErr
		}
		return ConnectErrorForDomain(err)
	}
	return nil
}

func (h *RunHandler) GetRun(ctx context.Context, req *connect.Request[agentcomposev2.GetRunRequest]) (*connect.Response[agentcomposev2.GetRunResponse], error) {
	if h.store == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("config store is required"))
	}
	runID := strings.TrimSpace(req.Msg.GetRunId())
	if runID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("run id is required"))
	}
	run, err := h.store.GetProjectRun(ctx, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if projectID := strings.TrimSpace(req.Msg.GetProjectId()); projectID != "" && run.ProjectID != projectID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("project run %s not found in project %s", runID, projectID))
	}
	return connect.NewResponse(&agentcomposev2.GetRunResponse{Run: ProjectRunDetailToProto(run)}), nil
}

func (h *RunHandler) ListRuns(ctx context.Context, req *connect.Request[agentcomposev2.ListRunsRequest]) (*connect.Response[agentcomposev2.ListRunsResponse], error) {
	if h.store == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("config store is required"))
	}
	runs, err := h.store.ListProjectRunsByOptions(ctx, domain.ProjectRunListOptions{
		ProjectID:   req.Msg.GetProjectId(),
		AgentName:   req.Msg.GetAgentName(),
		SandboxID:   req.Msg.GetSandboxId(),
		SchedulerID: req.Msg.GetSchedulerId(),
		Status:      ProjectRunStatusFromProto(req.Msg.GetStatus()),
		Source:      ProjectRunSourceFilterFromProto(req.Msg.GetSource()),
		Offset:      int(req.Msg.GetOffset()),
		Limit:       int(req.Msg.GetLimit()),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	items := make([]*agentcomposev2.RunSummary, 0, len(runs))
	for _, run := range runs {
		items = append(items, ProjectRunSummaryToProto(run))
	}
	return connect.NewResponse(&agentcomposev2.ListRunsResponse{Runs: items}), nil
}

func (h *RunHandler) FollowRunLogs(ctx context.Context, req *connect.Request[agentcomposev2.FollowRunLogsRequest], stream *connect.ServerStream[agentcomposev2.RunLogChunk]) error {
	if h.store == nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("config store is required"))
	}
	runID := strings.TrimSpace(req.Msg.GetRunId())
	if runID == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("run id is required"))
	}
	run, err := h.projectRunForLogRequest(ctx, req.Msg.GetProjectId(), runID)
	if err != nil {
		return err
	}
	offset, err := initialRunLogOffset(run.LogsPath, int(req.Msg.GetTailLines()), req.Msg.GetStartOffset(), req.Msg.GetFollow())
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if req.Msg.GetFollow() && h.runLogs != nil {
		return h.followRunLogsWithHub(ctx, req.Msg.GetProjectId(), run, offset, stream)
	}
	return h.followRunLogsByPolling(ctx, req, run, offset, stream)
}

func (h *RunHandler) followRunLogsByPolling(ctx context.Context, req *connect.Request[agentcomposev2.FollowRunLogsRequest], run domain.ProjectRunRecord, offset uint64, stream *connect.ServerStream[agentcomposev2.RunLogChunk]) error {
	runID := run.RunID
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var err error
		run, err = h.projectRunForLogRequest(ctx, req.Msg.GetProjectId(), runID)
		if err != nil {
			return err
		}
		if err := sendRunLogFileChunk(stream, run, &offset, time.Now().UTC()); err != nil {
			return err
		}
		if !req.Msg.GetFollow() || runs.StatusIsTerminal(run.Status) {
			return sendRunLogFinal(stream, run, offset)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *RunHandler) followRunLogsWithHub(ctx context.Context, projectID string, run domain.ProjectRunRecord, offset uint64, stream *connect.ServerStream[agentcomposev2.RunLogChunk]) error {
	sub := h.runLogs.Subscribe(run.RunID)
	if sub == nil {
		return h.followRunLogsByPolling(ctx, connect.NewRequest(&agentcomposev2.FollowRunLogsRequest{ProjectId: projectID, RunId: run.RunID, Follow: true}), run, offset, stream)
	}
	defer sub.Close()
	if err := sendRunLogFileChunk(stream, run, &offset, time.Now().UTC()); err != nil {
		return err
	}
	if runs.StatusIsTerminal(run.Status) {
		return sendRunLogFinal(stream, run, offset)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-sub.C():
			if !ok {
				return nil
			}
			if event.Offset <= offset {
				continue
			}
			current, err := h.projectRunForLogRequest(ctx, projectID, run.RunID)
			if err != nil {
				return err
			}
			if err := sendRunLogFileChunk(stream, current, &offset, event.CreatedAt); err != nil {
				return err
			}
		case <-ticker.C:
			current, err := h.projectRunForLogRequest(ctx, projectID, run.RunID)
			if err != nil {
				return err
			}
			if err := sendRunLogFileChunk(stream, current, &offset, time.Now().UTC()); err != nil {
				return err
			}
			if runs.StatusIsTerminal(current.Status) {
				return sendRunLogFinal(stream, current, offset)
			}
		}
	}
}

func sendRunLogFileChunk(stream *connect.ServerStream[agentcomposev2.RunLogChunk], run domain.ProjectRunRecord, offset *uint64, createdAt time.Time) error {
	data, nextOffset, err := readRunLogFromOffset(run.LogsPath, *offset)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return connect.NewError(connect.CodeInternal, err)
	}
	if data == "" {
		return nil
	}
	*offset = nextOffset
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if err := stream.Send(&agentcomposev2.RunLogChunk{
		Data:      data,
		Offset:    *offset,
		RunStatus: ProjectRunStatusToProto(run.Status),
		CreatedAt: FormatProjectTime(createdAt),
	}); err != nil {
		return connect.NewError(connect.CodeUnknown, err)
	}
	return nil
}

func sendRunLogFinal(stream *connect.ServerStream[agentcomposev2.RunLogChunk], run domain.ProjectRunRecord, offset uint64) error {
	return stream.Send(&agentcomposev2.RunLogChunk{
		Offset:    offset,
		IsFinal:   true,
		RunStatus: ProjectRunStatusToProto(run.Status),
		CreatedAt: FormatProjectTime(time.Now().UTC()),
	})
}

func (h *RunHandler) projectRunForLogRequest(ctx context.Context, projectID, runID string) (domain.ProjectRunRecord, error) {
	run, err := h.store.GetProjectRun(ctx, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ProjectRunRecord{}, connect.NewError(connect.CodeNotFound, err)
		}
		return domain.ProjectRunRecord{}, connect.NewError(connect.CodeInternal, err)
	}
	if projectID := strings.TrimSpace(projectID); projectID != "" && run.ProjectID != projectID {
		return domain.ProjectRunRecord{}, connect.NewError(connect.CodeNotFound, fmt.Errorf("project run %s not found in project %s", runID, projectID))
	}
	return run, nil
}

func initialRunLogOffset(path string, tailLines int, startOffset uint64, _ bool) (uint64, error) {
	if tailLines > 0 {
		return tailRunLogOffset(path, tailLines)
	}
	if startOffset > 0 {
		return startOffset, nil
	}
	return 0, nil
}

func readRunLogFromOffset(path string, offset uint64) (string, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", offset, err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(int64(offset), io.SeekStart); err != nil {
		return "", offset, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", offset, err
	}
	return string(data), offset + uint64(len(data)), nil
}

func tailRunLogOffset(path string, lines int) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if lines <= 0 || len(data) == 0 {
		return uint64(len(data)), nil
	}
	seen := 0
	for index := len(data) - 1; index >= 0; index-- {
		if data[index] != '\n' {
			continue
		}
		if index == len(data)-1 {
			continue
		}
		seen++
		if seen == lines {
			return uint64(index + 1), nil
		}
	}
	return 0, nil
}

func (h *RunHandler) StopRun(ctx context.Context, req *connect.Request[agentcomposev2.StopRunRequest]) (*connect.Response[agentcomposev2.StopRunResponse], error) {
	if h.store == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("config store is required"))
	}
	runID := strings.TrimSpace(req.Msg.GetRunId())
	if runID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("run id is required"))
	}
	if h.stopper != nil {
		stopped, err := h.stopper.StopActiveRun(ctx, runID, req.Msg.GetReason())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if stopped {
			run, err := h.store.GetProjectRun(ctx, runID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, connect.NewError(connect.CodeNotFound, err)
				}
				return nil, connect.NewError(connect.CodeInternal, err)
			}
			return connect.NewResponse(&agentcomposev2.StopRunResponse{
				Run:           ProjectRunDetailToProto(run),
				StopRequested: true,
			}), nil
		}
	}
	coordinator := runs.NewCoordinator(h.store, domain.StableProjectRunID)
	current, err := h.store.GetProjectRun(ctx, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if runs.StatusIsTerminal(current.Status) {
		return connect.NewResponse(&agentcomposev2.StopRunResponse{
			Run:           ProjectRunDetailToProto(current),
			StopRequested: false,
		}), nil
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	if reason == "" {
		reason = "stop requested"
	}
	run, err := coordinator.MarkCanceled(ctx, runs.TransitionRequest{
		RunID: runID,
		Error: reason,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentcomposev2.StopRunResponse{
		Run:           ProjectRunDetailToProto(run),
		StopRequested: true,
	}), nil
}
