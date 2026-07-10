package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"agent-compose/pkg/execution"
	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type AgentSessionStore interface {
	GetSandbox(context.Context, string) (*domain.Sandbox, error)
	ListEvents(context.Context, string) ([]domain.SandboxEvent, error)
}

type AgentDefinitionStore interface {
	GetAgentDefinition(context.Context, string) (domain.AgentDefinition, error)
}

type AgentExecutor interface {
	ExecuteAgentRequest(context.Context, *domain.Sandbox, execution.ExecuteAgentRequest) (domain.NotebookCell, domain.SandboxEvent, domain.SandboxEvent, error)
}

type AgentHandler struct {
	store     AgentSessionStore
	configDB  AgentDefinitionStore
	executor  AgentExecutor
	publisher LoaderTopicPublisher
}

func NewAgentHandler(store AgentSessionStore, configDB AgentDefinitionStore, executor AgentExecutor, publisher LoaderTopicPublisher) *AgentHandler {
	return &AgentHandler{store: store, configDB: configDB, executor: executor, publisher: publisher}
}

func (h *AgentHandler) SendAgentMessage(ctx context.Context, req *connect.Request[agentcomposev1.SendAgentMessageRequest]) (*connect.Response[agentcomposev1.SendAgentMessageResponse], error) {
	session, err := h.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if session.Summary.VMStatus != domain.VMStatusRunning {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("session is not running"))
	}
	message := strings.TrimSpace(req.Msg.GetMessage())
	if message == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("message is required"))
	}
	agentConfig := h.resolveSessionAgentConfig(ctx, session, req.Msg.GetAgent())
	cell, userEvent, assistantEvent, err := h.executor.ExecuteAgentRequest(ctx, session, execution.ExecuteAgentRequest{
		Agent:             agentConfig.Provider,
		AgentDefinitionID: agentConfig.AgentDefinitionID,
		Model:             agentConfig.Model,
		ProviderEnvItems:  agentConfig.EnvItems,
		Message:           message,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	h.publishLoaderTopic("agent-compose.agent.completed", loaders.CellTopicPayload(session.Summary.ID, cell, "api"))
	return connect.NewResponse(&agentcomposev1.SendAgentMessageResponse{UserEvent: SessionEventToProto(userEvent), AssistantEvent: SessionEventToProto(assistantEvent)}), nil
}

func (h *AgentHandler) SendAgentMessageStream(ctx context.Context, req *connect.Request[agentcomposev1.SendAgentMessageRequest], stream *connect.ServerStream[agentcomposev1.SendAgentMessageStreamResponse]) error {
	PrepareStreamingHeaders(stream.ResponseHeader())
	session, err := h.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if session.Summary.VMStatus != domain.VMStatusRunning {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("session is not running"))
	}
	message := strings.TrimSpace(req.Msg.GetMessage())
	if message == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("message is required"))
	}
	agentConfig := h.resolveSessionAgentConfig(ctx, session, req.Msg.GetAgent())

	streamErr := func(sendErr error) error {
		if sendErr == nil {
			return nil
		}
		return connect.NewError(connect.CodeUnknown, sendErr)
	}

	cell, userEvent, assistantEvent, err := h.executor.ExecuteAgentRequest(ctx, session, execution.ExecuteAgentRequest{
		Agent:             agentConfig.Provider,
		AgentDefinitionID: agentConfig.AgentDefinitionID,
		Model:             agentConfig.Model,
		ProviderEnvItems:  agentConfig.EnvItems,
		Message:           message,
		Stream: execution.AgentExecutionStream{
			OnStart: func(cell domain.NotebookCell) error {
				return streamErr(stream.Send(&agentcomposev1.SendAgentMessageStreamResponse{
					EventType: agentcomposev1.SendAgentMessageStreamEventType_SEND_AGENT_MESSAGE_STREAM_EVENT_TYPE_STARTED,
					Session:   SessionSummaryToProto(&session.Summary),
					Run:       AgentRunToProto(cell),
					RunId:     cell.ID,
				}))
			},
			OnChunk: func(cellID string, chunk domain.ExecChunk) error {
				return streamErr(stream.Send(&agentcomposev1.SendAgentMessageStreamResponse{
					EventType: agentcomposev1.SendAgentMessageStreamEventType_SEND_AGENT_MESSAGE_STREAM_EVENT_TYPE_OUTPUT,
					RunId:     cellID,
					Chunk:     chunk.Text,
					IsStderr:  domain.NormalizeStdioStream(chunk.Stream) == domain.StdioStderr,
				}))
			},
		},
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	loaded, err := h.store.GetSandbox(ctx, session.Summary.ID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	h.publishLoaderTopic("agent-compose.agent.completed", loaders.CellTopicPayload(session.Summary.ID, cell, "api"))
	return streamErr(stream.Send(&agentcomposev1.SendAgentMessageStreamResponse{
		EventType:      agentcomposev1.SendAgentMessageStreamEventType_SEND_AGENT_MESSAGE_STREAM_EVENT_TYPE_COMPLETED,
		Session:        SessionSummaryToProto(&loaded.Summary),
		Run:            AgentRunToProto(cell),
		RunId:          cell.ID,
		UserEvent:      SessionEventToProto(userEvent),
		AssistantEvent: SessionEventToProto(assistantEvent),
	}))
}

func (h *AgentHandler) ListSessionEvents(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.ListSessionEventsResponse], error) {
	events, err := h.store.ListEvents(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &agentcomposev1.ListSessionEventsResponse{SessionId: req.Msg.GetSessionId()}
	for _, event := range events {
		resp.Events = append(resp.Events, SessionEventToProto(event))
	}
	return connect.NewResponse(resp), nil
}

func (h *AgentHandler) resolveSessionAgentConfig(ctx context.Context, session *domain.Sandbox, requested string) execution.AgentConfig {
	provider := domain.NormalizeAgentKind(requested)
	config := execution.AgentConfig{Provider: provider}
	if session == nil || h.configDB == nil {
		return config
	}
	agentID := execution.SessionTagValue(session.Summary.Tags, domain.AgentSandboxTagID)
	if agentID == "" || !domain.SandboxHasAgentTag(session, agentID) {
		return config
	}
	agent, err := h.configDB.GetAgentDefinition(ctx, agentID)
	if err != nil {
		return config
	}
	return execution.AgentConfigFromDefinition(agent, provider)
}

func (h *AgentHandler) publishLoaderTopic(topic string, payload map[string]any) {
	if h == nil || h.publisher == nil {
		return
	}
	h.publisher.Publish(domain.LoaderTopicEvent{Topic: topic, Payload: payload, CreatedAt: time.Now().UTC()})
}
