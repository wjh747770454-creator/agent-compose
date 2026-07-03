package runs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"agent-compose/pkg/capabilities"
	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	"agent-compose/pkg/images"
	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sessions"
	"agent-compose/pkg/storage/sessionstore"
	"agent-compose/pkg/workspaces"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

var (
	ErrInvalidRequest     = errors.New("invalid run request")
	ErrRunAgentStreamSend = errors.New("run agent stream send failed")
)

type AgentExecutor interface {
	ExecuteAgentRequest(context.Context, *domain.Session, execution.ExecuteAgentRequest) (domain.NotebookCell, domain.SessionEvent, domain.SessionEvent, error)
}

type SessionDriver interface {
	StartSessionVM(context.Context, *domain.Session) error
	StopSessionVM(context.Context, *domain.Session) error
}

type TopicPublisher interface {
	Publish(domain.LoaderTopicEvent) bool
}

type DashboardNotifier interface {
	Notify(reason string)
}

type ControllerStore interface {
	Store
	PreparationStore
	workspaces.Store
}

type Controller struct {
	config    *appconfig.Config
	store     *sessionstore.Store
	configDB  ControllerStore
	driver    SessionDriver
	executor  AgentExecutor
	images    images.Backend
	cap       capabilities.Provider
	streams   *sessions.StreamBroker
	bus       TopicPublisher
	dashboard DashboardNotifier
}

type ControllerDependencies struct {
	Config    *appconfig.Config
	Store     *sessionstore.Store
	ConfigDB  ControllerStore
	Driver    SessionDriver
	Executor  AgentExecutor
	Images    images.Backend
	Cap       capabilities.Provider
	Streams   *sessions.StreamBroker
	Bus       TopicPublisher
	Dashboard DashboardNotifier
}

func NewController(deps ControllerDependencies) *Controller {
	return &Controller{
		config:    deps.Config,
		store:     deps.Store,
		configDB:  deps.ConfigDB,
		driver:    deps.Driver,
		executor:  deps.Executor,
		images:    deps.Images,
		cap:       deps.Cap,
		streams:   deps.Streams,
		bus:       deps.Bus,
		dashboard: deps.Dashboard,
	}
}

type RunAgentRequest struct {
	ProjectID        string
	AgentName        string
	Prompt           string
	Source           string
	SchedulerID      string
	TriggerID        string
	ClientRequestID  string
	Env              []*agentcomposev2.EnvVarSpec
	SessionID        string
	OutputSchemaJSON string
	CleanupPolicy    agentcomposev2.RunSessionCleanupPolicy
}

type StreamSink struct {
	SendStarted func(run domain.ProjectRunRecord, createdAt time.Time) error
	SendChunk   func(runID string, chunk domain.ExecChunk, createdAt time.Time) error
}

func PrepareStreamingHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Set("Cache-Control", "no-cache, no-transform")
	headers.Set("X-Accel-Buffering", "no")
}

func (c *Controller) RunProjectAgent(ctx context.Context, req RunAgentRequest, stream *StreamSink) (domain.ProjectRunRecord, error, error) {
	if c.configDB == nil {
		return domain.ProjectRunRecord{}, nil, fmt.Errorf("config store is required")
	}
	coordinator := NewCoordinator(c.configDB, domain.StableProjectRunID)
	run, err := coordinator.BeginRun(ctx, StartRequest{
		ProjectID:       req.ProjectID,
		AgentName:       req.AgentName,
		Source:          req.Source,
		SchedulerID:     req.SchedulerID,
		TriggerID:       req.TriggerID,
		Prompt:          req.Prompt,
		ClientRequestID: req.ClientRequestID,
	})
	if err != nil {
		return domain.ProjectRunRecord{}, nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	transitionCtx := context.WithoutCancel(ctx)
	prepared, err := c.prepareProjectRun(ctx, run, req.Env)
	if err != nil {
		run, markErr := coordinator.MarkFailed(transitionCtx, TransitionRequest{
			RunID: run.RunID,
			Error: fmt.Sprintf("workspace preparation failed: %v", err),
		})
		if markErr != nil {
			return domain.ProjectRunRecord{}, nil, markErr
		}
		return run, err, nil
	}
	sessionResult, err := c.ensureProjectRunSession(ctx, run, prepared, req.SessionID)
	if err != nil {
		transition := TransitionRequest{
			RunID: run.RunID,
			Error: fmt.Sprintf("session start failed: %v", err),
		}
		if sessionResult.Session != nil {
			transition.SessionID = sessionResult.Session.Summary.ID
		}
		run, markErr := coordinator.MarkFailed(transitionCtx, transition)
		if markErr != nil {
			return domain.ProjectRunRecord{}, nil, markErr
		}
		return run, err, nil
	}
	run, err = coordinator.MarkRunning(transitionCtx, run.RunID, sessionResult.Session.Summary.ID)
	if err != nil {
		return domain.ProjectRunRecord{}, nil, err
	}
	agentConfig, err := c.projectRunAgentConfig(ctx, run)
	if err != nil {
		run, markErr := coordinator.MarkFailed(transitionCtx, TransitionRequest{
			RunID:     run.RunID,
			SessionID: sessionResult.Session.Summary.ID,
			ExitCode:  1,
			Error:     fmt.Sprintf("agent execution failed: %v", err),
		})
		if markErr != nil {
			return domain.ProjectRunRecord{}, nil, markErr
		}
		return run, err, nil
	}
	if c.executor == nil {
		err = fmt.Errorf("executor is required")
		run, markErr := coordinator.MarkFailed(transitionCtx, TransitionRequest{
			RunID:     run.RunID,
			SessionID: sessionResult.Session.Summary.ID,
			ExitCode:  1,
			Error:     fmt.Sprintf("agent execution failed: %v", err),
		})
		if markErr != nil {
			return domain.ProjectRunRecord{}, nil, markErr
		}
		return run, err, nil
	}
	cell, _, _, execErr := c.executor.ExecuteAgentRequest(ctx, sessionResult.Session, execution.ExecuteAgentRequest{
		Agent:             agentConfig.Provider,
		AgentDefinitionID: run.ManagedAgentID,
		Model:             agentConfig.Model,
		RunID:             run.RunID,
		Message:           req.Prompt,
		OutputSchemaJSON:  req.OutputSchemaJSON,
		Stream:            projectRunAgentExecutionStream(run, stream),
	})
	transition := TransitionFromAgentCell(run, sessionResult.Session, cell, execErr)
	if execErr != nil || !cell.Success {
		run, err = coordinator.MarkFailed(transitionCtx, transition)
		if err != nil {
			return domain.ProjectRunRecord{}, nil, err
		}
		run = c.cleanupProjectRunSession(transitionCtx, coordinator, run, sessionResult.Session, req.CleanupPolicy)
		return run, execErr, nil
	}
	run, err = coordinator.MarkSucceeded(transitionCtx, transition)
	if err != nil {
		return domain.ProjectRunRecord{}, nil, err
	}
	run = c.cleanupProjectRunSession(transitionCtx, coordinator, run, sessionResult.Session, req.CleanupPolicy)
	return run, nil, nil
}

func (c *Controller) projectRunAgentConfig(ctx context.Context, run domain.ProjectRunRecord) (execution.AgentConfig, error) {
	agent, err := c.configDB.GetAgentDefinition(ctx, run.ManagedAgentID)
	if err != nil {
		return execution.AgentConfig{}, fmt.Errorf("resolve managed agent definition %s: %w", run.ManagedAgentID, err)
	}
	config := execution.AgentConfigFromDefinition(agent, domain.DefaultAgentProvider)
	if config.Provider == "" {
		config.Provider = domain.DefaultAgentProvider
	}
	return config, nil
}

func projectRunAgentExecutionStream(run domain.ProjectRunRecord, sink *StreamSink) execution.AgentExecutionStream {
	if sink == nil {
		return execution.AgentExecutionStream{}
	}
	return execution.AgentExecutionStream{
		OnStart: func(domain.NotebookCell) error {
			if sink.SendStarted == nil {
				return nil
			}
			return sink.SendStarted(run, time.Now().UTC())
		},
		OnChunk: func(_ string, chunk domain.ExecChunk) error {
			if sink.SendChunk == nil {
				return nil
			}
			return sink.SendChunk(run.RunID, chunk, time.Now().UTC())
		},
	}
}

func (c *Controller) prepareProjectRun(ctx context.Context, run domain.ProjectRunRecord, requestEnv []*agentcomposev2.EnvVarSpec) (Preparation, error) {
	return PrepareProjectRun(ctx, c.configDB, projectRunWorkspaceResolver{controller: c}, run, requestEnv)
}

func (c *Controller) ensureProjectRunSession(ctx context.Context, run domain.ProjectRunRecord, prepared Preparation, requestedSessionID string) (SessionResult, error) {
	if c == nil || c.config == nil || c.store == nil || c.driver == nil {
		return SessionResult{}, fmt.Errorf("session runtime dependencies are required")
	}
	tags := SessionTags(run)
	capabilityVars, capabilityTags := capabilities.BuildGatewaySessionVars(capabilities.ProxyTarget(c.cap), prepared.CapsetIDs)
	tags = append(tags, capabilityTags...)
	if sessionID := strings.TrimSpace(requestedSessionID); sessionID != "" {
		session, err := c.store.GetSession(ctx, sessionID)
		if err != nil {
			return SessionResult{}, fmt.Errorf("load session %s: %w", sessionID, err)
		}
		if session.Summary.VMStatus != domain.VMStatusRunning {
			driver, err := driverpkg.ResolveSessionRuntimeDriver(session.Summary.Driver, c.config.RuntimeDriver)
			if err != nil {
				return SessionResult{}, err
			}
			guestImage := driverpkg.ResolveSessionGuestImage(session.Summary.GuestImage, driverpkg.DefaultGuestImageForDriver(c.config, driver))
			if err := images.EnsureDriverImage(ctx, c.config, c.images, images.EnsureRequest{
				Driver:      driver,
				ImageRef:    guestImage,
				ProjectName: run.ProjectName,
				AgentName:   run.AgentName,
			}); err != nil {
				return SessionResult{Session: session}, err
			}
		}
		session.EnvItems = domain.MergeEnvItems(session.EnvItems, capabilityVars)
		session.Summary.Tags = MergeSessionTags(session.Summary.Tags, tags)
		if err := c.startProjectRunSession(ctx, session, "session.resumed", "session resumed for project run"); err != nil {
			return SessionResult{Session: session}, err
		}
		return SessionResult{Session: session}, nil
	}

	workspaceID := ""
	if prepared.Workspace != nil {
		workspaceID = strings.TrimSpace(prepared.Workspace.ID)
	}
	driver, err := driverpkg.ResolveSessionRuntimeDriver(run.Driver, c.config.RuntimeDriver)
	if err != nil {
		return SessionResult{}, err
	}
	guestImage := driverpkg.ResolveSessionGuestImage(run.ImageRef, driverpkg.DefaultGuestImageForDriver(c.config, driver))
	if err := images.EnsureDriverImage(ctx, c.config, c.images, images.EnsureRequest{
		Driver:      driver,
		ImageRef:    guestImage,
		ProjectName: run.ProjectName,
		AgentName:   run.AgentName,
	}); err != nil {
		return SessionResult{}, err
	}
	session, err := c.store.CreateSession(ctx,
		SessionTitle(run),
		"",
		driver,
		guestImage,
		workspaceID,
		domain.SessionTypeManual,
		prepared.Workspace,
		domain.MergeEnvItems(prepared.EnvItems, capabilityVars),
		tags,
	)
	if err != nil {
		return SessionResult{}, err
	}
	session.ProviderEnvItems = prepared.ProviderEnvItems
	if err := c.startProjectRunSession(ctx, session, "session.created", "session started for project run"); err != nil {
		return SessionResult{Session: session, Created: true}, err
	}
	return SessionResult{Session: session, Created: true}, nil
}

func (c *Controller) startProjectRunSession(ctx context.Context, session *domain.Session, eventType, eventMessage string) error {
	if session == nil {
		return fmt.Errorf("session is required")
	}
	if err := workspaces.PrepareSessionWorkspace(ctx, c.config, c.configDB, session); err != nil {
		session.Summary.VMStatus = domain.VMStatusFailed
		_ = c.store.UpdateSession(ctx, session)
		return err
	}
	writeCapabilityGuide(ctx, c.cap, c.store, c.streams, session, capabilities.SessionCapsets(session))
	if session.Summary.VMStatus != domain.VMStatusRunning {
		if err := c.driver.StartSessionVM(ctx, session); err != nil {
			session.Summary.VMStatus = domain.VMStatusFailed
			_ = c.store.UpdateSession(ctx, session)
			return err
		}
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := c.store.UpdateSession(ctx, session); err != nil {
		return err
	}
	c.publishProjectRunSessionStarted(ctx, session, eventType, eventMessage)
	loaded, err := c.store.GetSession(ctx, session.Summary.ID)
	if err != nil {
		return err
	}
	domain.RestoreSessionTransientFields(loaded, session)
	*session = *loaded
	return nil
}

func (c *Controller) publishProjectRunSessionStarted(ctx context.Context, session *domain.Session, eventType, message string) {
	if c.streams != nil {
		c.streams.PublishSessionUpdated(&session.Summary)
	}
	if c.dashboard != nil {
		c.dashboard.Notify("session_updated")
	}
	event := domain.SessionEvent{
		ID:        uuid.NewString(),
		Type:      eventType,
		Level:     "info",
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
	_ = c.store.AddEvent(ctx, session.Summary.ID, event)
	if c.streams != nil {
		c.streams.PublishEventAdded(session.Summary.ID, event)
	}
	if c.bus != nil {
		topic := "agent-compose.session.created"
		if eventType == "session.resumed" {
			topic = "agent-compose.session.resumed"
		}
		c.bus.Publish(domain.LoaderTopicEvent{
			Topic:     topic,
			Payload:   loaders.SessionTopicPayload(session, "project-run"),
			CreatedAt: time.Now().UTC(),
		})
	}
}

func (c *Controller) cleanupProjectRunSession(ctx context.Context, coordinator *Coordinator, run domain.ProjectRunRecord, session *domain.Session, policy agentcomposev2.RunSessionCleanupPolicy) domain.ProjectRunRecord {
	if !CleanupPolicyStopsSession(policy) || session == nil {
		return run
	}
	cleanupErr := c.stopProjectRunSession(ctx, session)
	if cleanupErr == nil {
		return run
	}
	updated, err := coordinator.TransitionRun(ctx, TransitionRequest{
		RunID:        run.RunID,
		Status:       run.Status,
		SessionID:    run.SessionID,
		CleanupError: cleanupErr.Error(),
	})
	if err != nil {
		return run
	}
	return updated
}

func (c *Controller) stopProjectRunSession(ctx context.Context, session *domain.Session) error {
	if c.store == nil {
		return fmt.Errorf("session store is required")
	}
	loaded, err := c.store.GetSession(ctx, session.Summary.ID)
	if err != nil {
		return err
	}
	if loaded.Summary.VMStatus != domain.VMStatusRunning {
		return nil
	}
	if c.driver == nil {
		return fmt.Errorf("session driver is required")
	}
	if err := c.driver.StopSessionVM(ctx, loaded); err != nil {
		return err
	}
	loaded.Summary.VMStatus = domain.VMStatusStopped
	if err := c.store.UpdateSession(ctx, loaded); err != nil {
		return err
	}
	event := domain.SessionEvent{ID: uuid.NewString(), Type: "session.stopped", Level: "info", Message: "session stopped", CreatedAt: time.Now().UTC()}
	_ = c.store.AddEvent(ctx, loaded.Summary.ID, event)
	if c.streams != nil {
		c.streams.PublishSessionUpdated(&loaded.Summary)
		c.streams.PublishEventAdded(loaded.Summary.ID, event)
	}
	return nil
}

func writeCapabilityGuide(ctx context.Context, provider capabilities.Provider, store *sessionstore.Store, streams *sessions.StreamBroker, session *domain.Session, capsetIDs []string) {
	ids := capabilities.NormalizeCapsetIDs(capsetIDs)
	if len(ids) == 0 || provider == nil || session == nil {
		return
	}
	catalogPath := capabilities.SessionGuidePath(session)
	if catalogPath == "" {
		return
	}
	var b strings.Builder
	rendered := false
	for _, id := range ids {
		guide, err := provider.CapabilityGuide(ctx, id)
		if err != nil {
			slog.Warn("capability guide render skipped", "capset", id, "session_id", session.Summary.ID, "error", err)
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
		slog.Warn("capability guide dir create failed", "session_id", session.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, store, streams, session.Summary.ID, "capability guide directory create failed")
		return
	}
	if err := os.WriteFile(catalogPath, []byte(content), 0o644); err != nil {
		slog.Warn("capability guide write failed", "session_id", session.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, store, streams, session.Summary.ID, "capability guide write failed")
	}
}

func recordCapabilityGuideWarning(ctx context.Context, store *sessionstore.Store, streams *sessions.StreamBroker, sessionID, message string) {
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	event := domain.SessionEvent{
		ID:        uuid.NewString(),
		Type:      "capability.guide.warning",
		Level:     "warning",
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.AddEvent(ctx, sessionID, event); err != nil {
		slog.Warn("capability guide warning event failed", "session_id", sessionID, "error", err)
		return
	}
	if streams != nil {
		streams.PublishEventAdded(sessionID, event)
	}
}
