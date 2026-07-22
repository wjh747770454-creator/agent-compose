package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"agent-compose/pkg/capproxy"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/runs"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type RuntimeDelegationSandboxResolver interface {
	ResolveCapabilitySandbox(context.Context, string) (capproxy.SandboxBinding, error)
}

type RuntimeDelegationRunStore interface {
	ListProjectRunsForSandbox(context.Context, string) ([]domain.ProjectRunRecord, error)
}

type RuntimeDelegationRunner interface {
	RunProjectAgent(context.Context, runs.RunAgentRequest, *runs.StreamSink) (domain.ProjectRunRecord, error, error)
}

type RuntimeDelegationAuditor interface {
	AppendProjectRunEvent(context.Context, domain.ProjectRunEventRecord) (domain.ProjectRunEventRecord, bool, error)
}

type RuntimeDelegationOptions struct {
	Sandboxes RuntimeDelegationSandboxResolver
	Runs      RuntimeDelegationRunStore
	Runner    RuntimeDelegationRunner
	Auditor   RuntimeDelegationAuditor
}

type runtimeDelegationRequest struct {
	TargetAgent      string `json:"targetAgent"`
	Prompt           string `json:"prompt"`
	OutputSchemaJSON string `json:"outputSchemaJson,omitempty"`
	IdempotencyKey   string `json:"idempotencyKey,omitempty"`
	Reason           string `json:"reason,omitempty"`
	TimeoutMs        int64  `json:"timeoutMs,omitempty"`
}

type runtimeDelegationTakeoverRequest struct {
	ChildRunID string `json:"childRunId,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type runtimeDelegationResponse struct {
	ChildRunID       string           `json:"childRunId,omitempty"`
	ParentRunID      string           `json:"parentRunId,omitempty"`
	RootRunID        string           `json:"rootRunId,omitempty"`
	DelegationID     string           `json:"delegationId,omitempty"`
	Attempt          int              `json:"attempt,omitempty"`
	AttemptRunIDs    []string         `json:"attemptRunIds,omitempty"`
	Status           string           `json:"status,omitempty"`
	Output           string           `json:"output,omitempty"`
	ResultJSON       string           `json:"resultJson,omitempty"`
	StructuredResult any              `json:"structuredResult,omitempty"`
	Warnings         []string         `json:"warnings,omitempty"`
	Error            *delegationError `json:"error,omitempty"`
}

type delegationError struct {
	Classification string `json:"classification"`
	Message        string `json:"message"`
}

func RegisterRuntimeDelegationRoutes(app *echo.Echo, opts RuntimeDelegationOptions) {
	handler := runtimeDelegationHandler{opts: opts}
	app.POST("/api/runtime/sandboxes/:sandbox_id/delegations", handler.handle)
	app.POST("/api/runtime/sandboxes/:sandbox_id/delegations/:delegation_id/takeover", handler.handleTakeover)
}

type runtimeDelegationHandler struct {
	opts RuntimeDelegationOptions
}

func (h runtimeDelegationHandler) handle(c echo.Context) error {
	if h.opts.Sandboxes == nil || h.opts.Runs == nil || h.opts.Runner == nil {
		return c.JSON(http.StatusInternalServerError, runtimeDelegationResponse{Error: newDelegationError("internal", "delegation dependencies are required")})
	}
	token := runtimeCapabilityToken(c.Request().Header)
	if token == "" {
		return c.JSON(http.StatusUnauthorized, runtimeDelegationResponse{Error: newDelegationError("authentication", "capability sandbox token is required")})
	}
	binding, err := h.opts.Sandboxes.ResolveCapabilitySandbox(c.Request().Context(), token)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, runtimeDelegationResponse{Error: newDelegationError("authentication", "invalid capability sandbox token")})
	}
	sandboxID := strings.TrimSpace(c.Param("sandbox_id"))
	if sandboxID == "" || binding.SandboxID != sandboxID {
		return c.JSON(http.StatusForbidden, runtimeDelegationResponse{Error: newDelegationError("authorization", "capability token is not valid for this sandbox")})
	}
	parent, err := h.activeParentRun(c.Request().Context(), sandboxID)
	if err != nil {
		return c.JSON(http.StatusConflict, runtimeDelegationResponse{Error: newDelegationError("parent_run", err.Error())})
	}
	var request runtimeDelegationRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, runtimeDelegationResponse{Error: newDelegationError("invalid_request", "invalid delegation request")})
	}
	request.TargetAgent = strings.TrimSpace(request.TargetAgent)
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.TargetAgent == "" || request.Prompt == "" {
		return c.JSON(http.StatusBadRequest, runtimeDelegationResponse{Error: newDelegationError("invalid_request", "targetAgent and prompt are required")})
	}
	request.OutputSchemaJSON = strings.TrimSpace(request.OutputSchemaJSON)
	if request.OutputSchemaJSON != "" {
		if !json.Valid([]byte(request.OutputSchemaJSON)) {
			return c.JSON(http.StatusBadRequest, runtimeDelegationResponse{Error: newDelegationError("invalid_request", "outputSchemaJson must be valid JSON")})
		}
		request.Prompt = delegationPromptWithSchema(request.Prompt, request.OutputSchemaJSON)
	}
	delegationID := strings.TrimSpace(request.IdempotencyKey)
	if delegationID == "" {
		delegationID = uuid.NewString()
	}
	rootRunID := parent.RootRunID
	if rootRunID == "" {
		rootRunID = parent.RunID
	}
	ctx := c.Request().Context()
	if request.TimeoutMs > 0 {
		timeout := time.Duration(request.TimeoutMs) * time.Millisecond
		if timeout > 30*time.Minute {
			timeout = 30 * time.Minute
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	h.appendDelegationEvent(ctx, parent, "delegation.created", request.TargetAgent, delegationID, rootRunID, 0, "", "", true)
	child, execErr, runErr, classification, attempt, attemptRunIDs := h.executeDelegation(ctx, parent, rootRunID, delegationID, request)
	response := runtimeDelegationResponse{
		ChildRunID: child.RunID, ParentRunID: parent.RunID, RootRunID: rootRunID,
		DelegationID: delegationID, Attempt: attempt, AttemptRunIDs: attemptRunIDs, Status: child.Status, Output: child.Output,
		ResultJSON: child.ResultJSON, Warnings: append([]string(nil), child.Warnings...),
	}
	if runErr != nil {
		response.Error = newDelegationError(classification, runErr.Error())
		return c.JSON(http.StatusBadRequest, response)
	}
	if execErr != nil || child.Status != domain.ProjectRunStatusSucceeded {
		message := child.Error
		if message == "" && execErr != nil {
			message = execErr.Error()
		}
		if message == "" {
			message = "delegated run failed"
		}
		response.Error = newDelegationError(classification, message)
		return c.JSON(http.StatusBadGateway, response)
	}
	if value := strings.TrimSpace(child.StructuredResultJSON); value != "" {
		var structured any
		if err := json.Unmarshal([]byte(value), &structured); err != nil {
			response.Error = newDelegationError("validation", "stored structured result is invalid JSON")
			return c.JSON(http.StatusBadGateway, response)
		}
		response.StructuredResult = structured
	}
	return c.JSON(http.StatusOK, response)
}

func (h runtimeDelegationHandler) handleTakeover(c echo.Context) error {
	if h.opts.Sandboxes == nil || h.opts.Runs == nil || h.opts.Auditor == nil {
		return c.JSON(http.StatusInternalServerError, runtimeDelegationResponse{Error: newDelegationError("internal", "delegation audit dependencies are required")})
	}
	token := runtimeCapabilityToken(c.Request().Header)
	if token == "" {
		return c.JSON(http.StatusUnauthorized, runtimeDelegationResponse{Error: newDelegationError("authentication", "capability sandbox token is required")})
	}
	binding, err := h.opts.Sandboxes.ResolveCapabilitySandbox(c.Request().Context(), token)
	if err != nil || binding.SandboxID != strings.TrimSpace(c.Param("sandbox_id")) {
		return c.JSON(http.StatusForbidden, runtimeDelegationResponse{Error: newDelegationError("authorization", "capability token is not valid for this sandbox")})
	}
	parent, err := h.activeParentRun(c.Request().Context(), binding.SandboxID)
	if err != nil {
		return c.JSON(http.StatusConflict, runtimeDelegationResponse{Error: newDelegationError("parent_run", err.Error())})
	}
	delegationID := strings.TrimSpace(c.Param("delegation_id"))
	if delegationID == "" {
		return c.JSON(http.StatusBadRequest, runtimeDelegationResponse{Error: newDelegationError("invalid_request", "delegation id is required")})
	}
	var request runtimeDelegationTakeoverRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, runtimeDelegationResponse{Error: newDelegationError("invalid_request", "invalid takeover request")})
	}
	rootRunID := parent.RootRunID
	if rootRunID == "" {
		rootRunID = parent.RunID
	}
	payload, _ := json.Marshal(map[string]any{
		"delegationId": delegationID, "parentRunId": parent.RunID, "rootRunId": rootRunID,
		"childRunId": strings.TrimSpace(request.ChildRunID), "reason": strings.TrimSpace(request.Reason),
	})
	_, _, _ = h.opts.Auditor.AppendProjectRunEvent(context.WithoutCancel(c.Request().Context()), domain.ProjectRunEventRecord{
		ID: uuid.NewString(), RunID: parent.RunID, Kind: domain.ProjectRunEventKindAgentActivity,
		Agent: parent.AgentName, Name: "delegation.taken_over", Text: strings.TrimSpace(request.Reason), PayloadJSON: string(payload), Success: true,
	})
	return c.JSON(http.StatusOK, map[string]any{"recorded": true, "delegationId": delegationID, "parentRunId": parent.RunID})
}

func (h runtimeDelegationHandler) executeDelegation(ctx context.Context, parent domain.ProjectRunRecord, rootRunID, delegationID string, request runtimeDelegationRequest) (domain.ProjectRunRecord, error, error, string, int, []string) {
	var child domain.ProjectRunRecord
	var execErr error
	var runErr error
	var classification string
	var attemptRunIDs []string
	for attempt := 1; attempt <= 2; attempt++ {
		clientRequestID := fmt.Sprintf("%s:%d", delegationID, attempt)
		predictedRunID, _ := domain.StableProjectRunID(parent.ProjectID, request.TargetAgent, domain.ProjectRunSourceAPI, clientRequestID)
		h.appendDelegationEvent(ctx, parent, "delegation.started", request.TargetAgent, delegationID, rootRunID, attempt, predictedRunID, "", true)
		child, execErr, runErr = h.opts.Runner.RunProjectAgent(ctx, runs.RunAgentRequest{
			ProjectID: parent.ProjectID, AgentName: request.TargetAgent, ParentRunID: parent.RunID, RootRunID: rootRunID,
			DelegationID: delegationID, DelegationAttempt: attempt, DelegationReason: strings.TrimSpace(request.Reason),
			Prompt: request.Prompt, Source: domain.ProjectRunSourceAPI, ClientRequestID: clientRequestID,
			// Delegated runs carry their schema in the prompt. Some OpenAI-compatible
			// facades close provider-native structured streams before response.completed.
			// The proxy extracts and validates JSON before accepting the child result.
			OutputSchemaJSON: "",
			CleanupPolicy:    agentcomposev2.RunSandboxCleanupPolicy_RUN_SANDBOX_CLEANUP_POLICY_REMOVE_ON_COMPLETION,
		}, nil)
		runID := child.RunID
		if runID == "" {
			runID = predictedRunID
		}
		attemptRunIDs = append(attemptRunIDs, runID)
		if runErr == nil && execErr == nil && child.Status == domain.ProjectRunStatusSucceeded && request.OutputSchemaJSON != "" {
			var normalized string
			normalized, execErr = extractDelegationStructuredResult(child.Output)
			if execErr == nil {
				child.Output = normalized
				child.StructuredResultJSON = normalized
			}
		}
		classification = classifyDelegationFailure(child, execErr, runErr)
		if runErr == nil && execErr == nil && child.Status == domain.ProjectRunStatusSucceeded {
			h.appendDelegationEvent(ctx, parent, "delegation.succeeded", request.TargetAgent, delegationID, rootRunID, attempt, runID, "", true)
			return child, nil, nil, "", attempt, attemptRunIDs
		}
		h.appendDelegationEvent(ctx, parent, "delegation.failed", request.TargetAgent, delegationID, rootRunID, attempt, runID, classification, false)
		if attempt == 1 && classification == "transient" && ctx.Err() == nil {
			h.appendDelegationEvent(ctx, parent, "delegation.retried", request.TargetAgent, delegationID, rootRunID, attempt+1, runID, classification, true)
			continue
		}
		return child, execErr, runErr, classification, attempt, attemptRunIDs
	}
	return child, execErr, runErr, classification, 2, attemptRunIDs
}

func delegationPromptWithSchema(prompt, schema string) string {
	return strings.TrimSpace(prompt) + `

## Structured output contract (not business evidence)
Return exactly one JSON object. The first non-whitespace character must be "{" and the last must be "}".
Do not add Markdown fences, commentary, progress text, or fields not allowed by this JSON Schema:
` + strings.TrimSpace(schema)
}

func extractDelegationStructuredResult(output string) (string, error) {
	value := strings.TrimSpace(output)
	if value == "" {
		return "", errors.New("delegated structured output is empty")
	}
	starts := []int{0}
	for index, char := range value {
		if char == '{' && index != 0 {
			starts = append(starts, index)
		}
	}
	for _, start := range starts {
		decoder := json.NewDecoder(strings.NewReader(value[start:]))
		var result map[string]any
		if err := decoder.Decode(&result); err != nil || result == nil {
			continue
		}
		normalized, err := json.Marshal(result)
		if err == nil {
			return string(normalized), nil
		}
	}
	return "", errors.New("delegated structured output is missing or invalid JSON")
}

func classifyDelegationFailure(child domain.ProjectRunRecord, execErr, runErr error) string {
	if runErr != nil {
		return "invalid_request"
	}
	if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
		return "timeout"
	}
	message := strings.ToLower(strings.Join([]string{child.Error, errorText(execErr)}, " "))
	for _, fragment := range []string{"sandbox start", "workspace preparation", "temporarily", "unavailable", "connection reset", "connection refused", "timeout"} {
		if strings.Contains(message, fragment) {
			return "transient"
		}
	}
	if strings.Contains(message, "structured output") || strings.Contains(message, "validation") {
		return "validation"
	}
	return "execution"
}

func (h runtimeDelegationHandler) appendDelegationEvent(ctx context.Context, parent domain.ProjectRunRecord, name, targetAgent, delegationID, rootRunID string, attempt int, childRunID, classification string, success bool) {
	if h.opts.Auditor == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"delegationId": delegationID, "parentRunId": parent.RunID, "rootRunId": rootRunID,
		"childRunId": childRunID, "targetAgent": targetAgent, "attempt": attempt, "errorClassification": classification,
	})
	if err != nil {
		return
	}
	_, _, _ = h.opts.Auditor.AppendProjectRunEvent(context.WithoutCancel(ctx), domain.ProjectRunEventRecord{
		ID: uuid.NewString(), RunID: parent.RunID, Kind: domain.ProjectRunEventKindAgentActivity,
		Agent: parent.AgentName, Name: name, PayloadJSON: string(payload), Success: success,
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (h runtimeDelegationHandler) activeParentRun(ctx context.Context, sandboxID string) (domain.ProjectRunRecord, error) {
	items, err := h.opts.Runs.ListProjectRunsForSandbox(ctx, sandboxID)
	if err != nil {
		return domain.ProjectRunRecord{}, fmt.Errorf("resolve parent run: %w", err)
	}
	var active []domain.ProjectRunRecord
	for _, item := range items {
		if item.Status == domain.ProjectRunStatusRunning {
			active = append(active, item)
		}
	}
	if len(active) != 1 {
		return domain.ProjectRunRecord{}, fmt.Errorf("sandbox must have exactly one active parent run; found %d", len(active))
	}
	return active[0], nil
}

func runtimeCapabilityToken(header http.Header) string {
	value := strings.TrimSpace(header.Get(capproxy.SandboxTokenMetadata))
	if value != "" {
		return value
	}
	value = strings.TrimSpace(header.Get("Authorization"))
	if len(value) > 7 && strings.EqualFold(value[:7], "Bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return ""
}

func newDelegationError(classification, message string) *delegationError {
	return &delegationError{Classification: classification, Message: message}
}
