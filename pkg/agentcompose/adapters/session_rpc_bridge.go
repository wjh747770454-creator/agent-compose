package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"agent-compose/pkg/agentcompose/api"
	"agent-compose/pkg/capabilities"
	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/dashboard"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/llms"
	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sessions"
	"agent-compose/pkg/storage/configstore"
	"agent-compose/pkg/storage/sessionstore"
	"agent-compose/pkg/workspaces"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type SandboxRPCBridge struct {
	config    *appconfig.Config
	store     *sessionstore.Store
	configDB  *configstore.ConfigStore
	driver    sessions.SandboxDriver
	runtimes  RuntimeProvider
	bus       *loaders.Bus
	streams   *sessions.StreamBroker
	cap       capabilities.Provider
	capTokens *CapabilitySandboxResolver
	dashboard *dashboard.Hub
}

func NewSandboxRPCBridge(config *appconfig.Config, store *sessionstore.Store, configDB *configstore.ConfigStore, driver sessions.SandboxDriver, runtimes RuntimeProvider, bus *loaders.Bus, streams *sessions.StreamBroker, cap capabilities.Provider, capTokens *CapabilitySandboxResolver, dashboard *dashboard.Hub) *SandboxRPCBridge {
	return &SandboxRPCBridge{
		config:    config,
		store:     store,
		configDB:  configDB,
		driver:    driver,
		runtimes:  runtimes,
		bus:       bus,
		streams:   streams,
		cap:       cap,
		capTokens: capTokens,
		dashboard: dashboard,
	}
}

func (b *SandboxRPCBridge) CallJSON(ctx context.Context, method, requestJSON string) (string, error) {
	return b.CallJSONWithSource(ctx, method, requestJSON, domain.SandboxTypeScript)
}

func (b *SandboxRPCBridge) CallJSONWithSource(ctx context.Context, method, requestJSON, source string) (string, error) {
	method = strings.TrimSpace(method)
	switch method {
	case "CreateSession":
		var req agentcomposev1.CreateSessionRequest
		if err := unmarshalSessionProtoJSON(requestJSON, &req); err != nil {
			return "", err
		}
		resp, err := b.createSession(ctx, connect.NewRequest(&req), source)
		if err != nil {
			return "", err
		}
		return marshalSessionProtoJSON(resp.Msg)
	case "ResumeSession":
		var req agentcomposev1.SessionIDRequest
		if err := unmarshalSessionProtoJSON(requestJSON, &req); err != nil {
			return "", err
		}
		resp, err := b.resumeSession(ctx, connect.NewRequest(&req), source)
		if err != nil {
			return "", err
		}
		return marshalSessionProtoJSON(resp.Msg)
	case "StopSession":
		var req agentcomposev1.SessionIDRequest
		if err := unmarshalSessionProtoJSON(requestJSON, &req); err != nil {
			return "", err
		}
		resp, err := b.stopSession(ctx, connect.NewRequest(&req), source)
		if err != nil {
			return "", err
		}
		return marshalSessionProtoJSON(resp.Msg)
	case "GetSession":
		var req agentcomposev1.SessionIDRequest
		if err := unmarshalSessionProtoJSON(requestJSON, &req); err != nil {
			return "", err
		}
		resp, err := b.GetSession(ctx, connect.NewRequest(&req))
		if err != nil {
			return "", err
		}
		return marshalSessionProtoJSON(resp.Msg)
	case "ListSessions":
		var req agentcomposev1.ListSessionsRequest
		if err := unmarshalSessionProtoJSON(requestJSON, &req); err != nil {
			return "", err
		}
		resp, err := b.ListSessions(ctx, connect.NewRequest(&req))
		if err != nil {
			return "", err
		}
		return marshalSessionProtoJSON(resp.Msg)
	case "GetSessionProxy":
		var req agentcomposev1.SessionIDRequest
		if err := unmarshalSessionProtoJSON(requestJSON, &req); err != nil {
			return "", err
		}
		resp, err := b.GetSessionProxy(ctx, connect.NewRequest(&req))
		if err != nil {
			return "", err
		}
		return marshalSessionProtoJSON(resp.Msg)
	default:
		return "", fmt.Errorf("unsupported session rpc %q", method)
	}
}

func unmarshalSessionProtoJSON(raw string, msg proto.Message) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if err := protojson.Unmarshal([]byte(raw), msg); err != nil {
		return fmt.Errorf("decode session rpc request: %w", err)
	}
	return nil
}

func marshalSessionProtoJSON(msg proto.Message) (string, error) {
	if msg == nil {
		return "", nil
	}
	data, err := protojson.MarshalOptions{}.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("encode session rpc response: %w", err)
	}
	return string(data), nil
}

func (b *SandboxRPCBridge) publishLoaderTopic(topic string, payload map[string]any) {
	if b == nil || b.bus == nil {
		return
	}
	b.bus.Publish(domain.LoaderTopicEvent{
		Topic:     topic,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
}

func (b *SandboxRPCBridge) CreateSession(ctx context.Context, req *connect.Request[agentcomposev1.CreateSessionRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	return b.createSession(ctx, req, domain.SandboxTypeManual)
}

func (b *SandboxRPCBridge) createSession(ctx context.Context, req *connect.Request[agentcomposev1.CreateSessionRequest], source string) (*connect.Response[agentcomposev1.SessionResponse], error) {
	tags := make([]domain.SandboxTag, 0, len(req.Msg.GetTags()))
	for _, tag := range req.Msg.GetTags() {
		tags = append(tags, domain.SandboxTag{Name: tag.GetName(), Value: tag.GetValue()})
	}
	envItems := make([]domain.SandboxEnvVar, 0, len(req.Msg.GetEnvItems()))
	for _, item := range req.Msg.GetEnvItems() {
		envItems = append(envItems, domain.SandboxEnvVar{Name: item.GetName(), Value: item.GetValue(), Secret: item.GetSecret()})
	}
	globalEnvItems, err := b.configDB.ListGlobalEnv(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	envItems = domain.MergeEnvItems(globalEnvItems, envItems)
	providerEnvItems := envItems
	envItems = llms.FilterPersistedRuntimeEnv(envItems)
	capabilityVars, capabilityTags := capabilities.BuildGatewaySandboxVars(capabilities.ProxyTarget(b.cap), req.Msg.GetCapsetIds())
	envItems = domain.MergeEnvItems(envItems, capabilityVars)
	tags = append(tags, capabilityTags...)

	var workspaceSnapshot *domain.SandboxWorkspace
	workspaceID := strings.TrimSpace(req.Msg.GetWorkspaceId())
	if workspaceID != "" {
		workspaceConfig, err := b.configDB.GetWorkspaceConfig(ctx, workspaceID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		workspaceSnapshot = toSandboxWorkspaceSnapshot(workspaceConfig)
	}

	driver, err := driverpkg.ResolveSandboxRuntimeDriver(req.Msg.GetDriver(), b.config.RuntimeDriver)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	guestImage := driverpkg.ResolveSandboxGuestImage(req.Msg.GetGuestImage(), driverpkg.DefaultGuestImageForDriver(b.config, driver))
	session, err := b.store.CreateSandbox(ctx, req.Msg.GetTitle(), req.Msg.GetBaseWorkspace(), driver, guestImage, workspaceID, source, workspaceSnapshot, envItems, tags)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	session.ProviderEnvItems = providerEnvItems
	if err := workspaces.PrepareSessionWorkspace(ctx, b.config, b.configDB, session); err != nil {
		session.Summary.VMStatus = domain.VMStatusFailed
		_ = b.store.UpdateSandbox(ctx, session)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	writeCapabilityGuide(ctx, b.cap, b.store, b.streams, session, req.Msg.GetCapsetIds())
	if err := b.driver.StartSandboxVM(ctx, session); err != nil {
		session.Summary.VMStatus = domain.VMStatusFailed
		_ = b.store.UpdateSandbox(ctx, session)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := b.store.UpdateSandbox(ctx, session); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	b.streams.PublishSandboxUpdated(&session.Summary)
	if b.dashboard != nil {
		b.dashboard.Notify("sandbox_updated")
	}
	event := domain.SandboxEvent{
		ID:        uuid.NewString(),
		Type:      "sandbox.created",
		Level:     "info",
		Message:   fmt.Sprintf("sandbox started with %s driver using guest image %s", session.Summary.Driver, session.Summary.GuestImage),
		CreatedAt: time.Now().UTC(),
	}
	_ = b.store.AddEvent(ctx, session.Summary.ID, event)
	b.streams.PublishEventAdded(session.Summary.ID, event)
	loaded, err := b.store.GetSandbox(ctx, session.Summary.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	domain.RestoreSandboxTransientFields(loaded, session)
	b.indexCapabilitySandbox(loaded)
	b.publishLoaderTopic("agent-compose.session.created", loaders.SessionTopicPayload(loaded, source))
	return connect.NewResponse(&agentcomposev1.SessionResponse{Session: api.SessionDetailToProto(loaded)}), nil
}

func (b *SandboxRPCBridge) ResumeSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	return b.resumeSession(ctx, req, domain.SandboxTypeManual)
}

func (b *SandboxRPCBridge) resumeSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest], source string) (*connect.Response[agentcomposev1.SessionResponse], error) {
	session, err := b.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	loaded, err := b.sessionLifecycle().ResumeLoaded(ctx, session, capabilities.SandboxCapsets(session))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	b.indexCapabilitySandbox(loaded)
	b.publishLoaderTopic("agent-compose.session.resumed", loaders.SessionTopicPayload(loaded, source))
	return connect.NewResponse(&agentcomposev1.SessionResponse{Session: api.SessionDetailToProto(loaded)}), nil
}

func (b *SandboxRPCBridge) ReconcileRuntimeState(ctx context.Context, session *domain.Sandbox) (*domain.Sandbox, error) {
	reconciled, err := b.sessionLifecycle().ReconcileRuntimeState(ctx, session)
	if err == nil && reconciled != nil && reconciled.Summary.VMStatus != domain.VMStatusRunning {
		b.revokeCapabilitySandbox(reconciled.Summary.ID)
	}
	return reconciled, err
}

func (b *SandboxRPCBridge) StopSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	return b.stopSession(ctx, req, domain.SandboxTypeManual)
}

func (b *SandboxRPCBridge) stopSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest], source string) (*connect.Response[agentcomposev1.SessionResponse], error) {
	session, err := b.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if reconciled, recErr := b.ReconcileRuntimeState(ctx, session); recErr != nil {
		slog.Warn("failed to reconcile sandbox runtime state before stop", "sandbox_id", session.Summary.ID, "error", recErr)
	} else {
		session = reconciled
	}
	if session.Summary.VMStatus != domain.VMStatusRunning {
		b.revokeCapabilitySandbox(session.Summary.ID)
		return connect.NewResponse(&agentcomposev1.SessionResponse{Session: api.SessionDetailToProto(session)}), nil
	}
	loaded, stopped, err := b.sessionLifecycle().StopLoaded(ctx, session)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	b.revokeCapabilitySandbox(loaded.Summary.ID)
	if stopped {
		b.publishLoaderTopic("agent-compose.session.stopped", loaders.SessionTopicPayload(loaded, source))
	}
	return connect.NewResponse(&agentcomposev1.SessionResponse{Session: api.SessionDetailToProto(loaded)}), nil
}

func (b *SandboxRPCBridge) indexCapabilitySandbox(session *domain.Sandbox) {
	if b != nil && b.capTokens != nil {
		b.capTokens.IndexSandbox(session)
	}
}

func (b *SandboxRPCBridge) revokeCapabilitySandbox(sandboxID string) {
	if b != nil && b.capTokens != nil {
		b.capTokens.RevokeSandbox(sandboxID)
	}
}

func (b *SandboxRPCBridge) GetSession(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionResponse], error) {
	session, err := b.store.GetSandbox(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if reconciled, recErr := b.ReconcileRuntimeState(ctx, session); recErr != nil {
		slog.Warn("failed to reconcile sandbox runtime state during get", "sandbox_id", session.Summary.ID, "error", recErr)
	} else {
		session = reconciled
	}
	return connect.NewResponse(&agentcomposev1.SessionResponse{Session: api.SessionDetailToProto(session)}), nil
}

func (b *SandboxRPCBridge) ListSessions(ctx context.Context, req *connect.Request[agentcomposev1.ListSessionsRequest]) (*connect.Response[agentcomposev1.ListSessionsResponse], error) {
	options, err := api.SessionListOptionsFromProto(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	result, err := b.store.ListSandboxes(ctx, options)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &agentcomposev1.ListSessionsResponse{
		TotalCount: uint32(result.TotalCount),
		HasMore:    result.HasMore,
		NextOffset: uint32(result.NextOffset),
	}
	for _, session := range result.Sandboxes {
		if reconciled, recErr := b.ReconcileRuntimeState(ctx, session); recErr != nil {
			slog.Warn("failed to reconcile sandbox runtime state during list", "sandbox_id", session.Summary.ID, "error", recErr)
		} else {
			session = reconciled
		}
		resp.Sessions = append(resp.Sessions, api.SessionSummaryToProto(&session.Summary))
	}
	return connect.NewResponse(resp), nil
}

func (b *SandboxRPCBridge) EnsureSessionProxyReady(ctx context.Context, sessionID string) (domain.ProxyState, error) {
	_, proxyState, err := b.sessionLifecycle().EnsureProxyReady(ctx, sessionID)
	return proxyState, err
}

func (b *SandboxRPCBridge) GetSessionProxy(ctx context.Context, req *connect.Request[agentcomposev1.SessionIDRequest]) (*connect.Response[agentcomposev1.SessionProxyResponse], error) {
	session, proxyState, err := b.sessionLifecycle().EnsureProxyReady(ctx, req.Msg.GetSessionId())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	notebookURL := session.Summary.ProxyPath
	if proxyState.Token != "" {
		notebookURL += "?token=" + url.QueryEscape(proxyState.Token)
	}
	return connect.NewResponse(&agentcomposev1.SessionProxyResponse{
		SessionId:   session.Summary.ID,
		ProxyPath:   session.Summary.ProxyPath,
		NotebookUrl: notebookURL,
		Driver:      session.Summary.Driver,
		VmStatus:    session.Summary.VMStatus,
	}), nil
}

func (b *SandboxRPCBridge) sessionLifecycle() sessions.Lifecycle {
	return sessions.Lifecycle{
		Config:       b.config,
		Store:        b.store,
		Workspace:    b.configDB,
		Driver:       b.driver,
		Liveness:     sandboxRuntimeLiveness{runtimes: b.runtimes},
		TokenRevoker: b.configDB,
		Notifier: sandboxLifecycleNotifier{
			streams:   b.streams,
			dashboard: b.dashboard,
		},
		GuideWriter: func(ctx context.Context, session *domain.Sandbox, capsetIDs []string) {
			writeCapabilityGuide(ctx, b.cap, b.store, b.streams, session, capsetIDs)
		},
	}
}

type sandboxRuntimeLiveness struct {
	runtimes RuntimeProvider
}

func (p sandboxRuntimeLiveness) IsSandboxAlive(ctx context.Context, driver string, session *domain.Sandbox, vmState domain.VMState) (bool, bool, error) {
	if p.runtimes == nil {
		return false, false, nil
	}
	runtime, err := p.runtimes.ForDriver(driver)
	if err != nil {
		return false, false, err
	}
	aliveRuntime, ok := runtime.(interface {
		IsSandboxAlive(context.Context, *domain.Sandbox, domain.VMState) (bool, error)
	})
	if !ok {
		return false, false, nil
	}
	alive, err := aliveRuntime.IsSandboxAlive(ctx, session, vmState)
	return alive, true, err
}

type sandboxLifecycleNotifier struct {
	streams   *sessions.StreamBroker
	dashboard *dashboard.Hub
}

func (n sandboxLifecycleNotifier) PublishSandboxUpdated(summary *domain.SandboxSummary) {
	if n.streams != nil {
		n.streams.PublishSandboxUpdated(summary)
	}
}

func (n sandboxLifecycleNotifier) PublishEventAdded(sessionID string, event domain.SandboxEvent) {
	if n.streams != nil {
		n.streams.PublishEventAdded(sessionID, event)
	}
}

func (n sandboxLifecycleNotifier) NotifyDashboard(reason string) {
	if n.dashboard != nil {
		n.dashboard.Notify(reason)
	}
}

func writeCapabilityGuide(ctx context.Context, provider capabilities.Provider, store *sessionstore.Store, streams *sessions.StreamBroker, session *domain.Sandbox, capsetIDs []string) {
	ids := capabilities.NormalizeCapsetIDs(capsetIDs)
	if len(ids) == 0 || provider == nil || session == nil {
		return
	}
	catalogPath := capabilities.SandboxGuidePath(session)
	if catalogPath == "" {
		return
	}
	var b strings.Builder
	rendered := false
	for _, id := range ids {
		guide, err := provider.CapabilityGuide(ctx, id)
		if err != nil {
			slog.Warn("capability guide render skipped", "capset", id, "sandbox_id", session.Summary.ID, "error", err)
			recordCapabilityGuideWarning(ctx, store, streams, session.Summary.ID, fmt.Sprintf("capability guide render skipped for capset %s", id))
			continue
		}
		if rendered {
			b.WriteString("\n\n")
		}
		b.Write(guide)
		rendered = true
	}
	if !rendered {
		return
	}
	content := b.String()
	if preamble := capabilities.GuidePreamble(capabilities.ProxyTarget(provider)); preamble != "" {
		content = preamble + content
	}
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		slog.Warn("capability guide dir create failed", "sandbox_id", session.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, store, streams, session.Summary.ID, "capability guide directory create failed")
		return
	}
	if err := os.WriteFile(catalogPath, []byte(content), 0o644); err != nil {
		slog.Warn("capability guide write failed", "sandbox_id", session.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, store, streams, session.Summary.ID, "capability guide write failed")
	}
}

func recordCapabilityGuideWarning(ctx context.Context, store *sessionstore.Store, streams *sessions.StreamBroker, sessionID, message string) {
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	event := domain.SandboxEvent{
		ID:        uuid.NewString(),
		Type:      "capability.guide.warning",
		Level:     "warning",
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.AddEvent(ctx, sessionID, event); err != nil {
		slog.Warn("capability guide warning event failed", "sandbox_id", sessionID, "error", err)
		return
	}
	if streams != nil {
		streams.PublishEventAdded(sessionID, event)
	}
}

func toSandboxWorkspaceSnapshot(item domain.WorkspaceConfig) *domain.SandboxWorkspace {
	return &domain.SandboxWorkspace{
		ID:         item.ID,
		Name:       item.Name,
		Type:       item.Type,
		ConfigJSON: item.ConfigJSON,
	}
}
