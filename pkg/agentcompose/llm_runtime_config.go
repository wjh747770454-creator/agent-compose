package agentcompose

import (
	appconfig "agent-compose/pkg/config"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// llmFacadeTokenSourceAgent marks a per-agent-run facade token. Such a token is
// only used for the duration of a single bounded agent run, so the caller is
// expected to delete it when that run completes (see executeAgentRun) rather
// than letting it live for the whole session lifetime.
const llmFacadeTokenSourceAgent = "agent"

func ensureSessionLLMFacadeConfig(ctx context.Context, config *appconfig.Config, configDB *ConfigStore, session *Session, agent, model, source, runID string) (map[string]string, error) {
	if config == nil || configDB == nil || session == nil {
		return nil, nil
	}
	if normalizeAgentKind(agent) == "claude" {
		return ensureSessionClaudeLLMFacadeConfig(ctx, config, configDB, session, model, source, runID)
	}
	return ensureSessionCodexLLMFacadeConfig(ctx, config, configDB, session, model, source, runID)
}

func ensureSessionCodexLLMFacadeConfig(ctx context.Context, config *appconfig.Config, configDB *ConfigStore, session *Session, model, source, runID string) (map[string]string, error) {
	target, err := resolveRuntimeLLMTargetWithEnv(ctx, config, configDB, session.Summary.ID, llmProviderFamilyOpenAI, model, "", sessionLLMProviderEnvItems(session))
	if err != nil {
		if strings.Contains(err.Error(), "llm model is required") || strings.Contains(err.Error(), "llm provider is not configured") {
			return nil, nil
		}
		return nil, err
	}
	baseURL := guestRuntimeLLMBaseURL(config, session)
	if strings.TrimSpace(baseURL) == "" {
		return nil, nil
	}
	facadeWireAPI := llmAPIProtocolResponses
	tokenValue, token, err := newLLMFacadeToken(session.Summary.ID, target.Model.Name, target.Provider.ID, facadeWireAPI, source, runID)
	if err != nil {
		return nil, err
	}
	if err := configDB.SaveLLMFacadeToken(ctx, token); err != nil {
		return nil, err
	}
	openAIBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sessions/" + session.Summary.ID + "/llm/openai/v1"
	if err := writeCodexLLMConfig(session, target.Model.Name, openAIBaseURL, facadeWireAPI); err != nil {
		return nil, err
	}
	return map[string]string{
		"AGENT_COMPOSE_SESSION_TOKEN": tokenValue,
		"LLM_API_ENDPOINT":            openAIBaseURL,
		"LLM_API_KEY":                 tokenValue,
		"LLM_API_PROTOCOL":            facadeWireAPI,
		"OPENAI_API_KEY":              tokenValue,
		"OPENAI_BASE_URL":             openAIBaseURL,
	}, nil
}

func ensureSessionClaudeLLMFacadeConfig(ctx context.Context, config *appconfig.Config, configDB *ConfigStore, session *Session, model, source, runID string) (map[string]string, error) {
	baseURL := guestRuntimeLLMBaseURL(config, session)
	if strings.TrimSpace(baseURL) == "" {
		return nil, nil
	}
	providerEnv := sessionLLMProviderEnvItems(session)
	target, err := resolveRuntimeLLMTargetWithEnv(ctx, config, configDB, session.Summary.ID, llmProviderFamilyAnthropic, model, "", providerEnv)
	tokenModel := ""
	tokenProvider := ""
	if err != nil {
		if !isOptionalLLMFacadeConfigError(err) || !hasAnthropicProviderKey(ctx, config, configDB) {
			return nil, err
		}
	} else {
		tokenModel = target.Model.Name
		tokenProvider = target.Provider.ID
	}
	tokenValue, token, err := newLLMFacadeToken(session.Summary.ID, tokenModel, tokenProvider, llmAPIProtocolMessages, source, runID)
	if err != nil {
		return nil, err
	}
	if err := configDB.SaveLLMFacadeToken(ctx, token); err != nil {
		return nil, err
	}
	anthropicBaseURL := strings.TrimRight(baseURL, "/") + "/api/runtime/sessions/" + session.Summary.ID + "/llm/anthropic"
	env := map[string]string{
		"AGENT_COMPOSE_SESSION_TOKEN": tokenValue,
		"LLM_API_ENDPOINT":            anthropicBaseURL,
		"LLM_API_KEY":                 tokenValue,
		"LLM_API_PROTOCOL":            llmAPIProtocolMessages,
		"ANTHROPIC_API_KEY":           tokenValue,
		"ANTHROPIC_BASE_URL":          anthropicBaseURL,
	}
	if tokenModel != "" {
		env["ANTHROPIC_MODEL"] = tokenModel
		env["CLAUDE_MODEL"] = tokenModel
	}
	return env, nil
}

func isOptionalLLMFacadeConfigError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "llm model is required") ||
		strings.Contains(message, "llm provider is not configured") ||
		strings.Contains(message, "is not configured for provider family")
}

// hasAnthropicProviderKey reports whether a daemon-level Anthropic credential
// exists. It intentionally ignores per-session env items: request-time provider
// resolution runs without session env, so a session-scoped key (without a model
// to persist a provider) would never let a runtime request resolve. Tolerating a
// missing model is only safe when a daemon-level key can bootstrap a provider
// from the request's model at call time.
func hasAnthropicProviderKey(ctx context.Context, config *appconfig.Config, configDB *ConfigStore) bool {
	configKey := ""
	if config != nil {
		configKey = config.LLMAPIKey
	}
	return strings.TrimSpace(firstNonEmpty(
		lookupEnvValue(ctx, configDB, "ANTHROPIC_API_KEY"),
		lookupEnvValue(ctx, configDB, "ANTHROPIC_AUTH_TOKEN"),
		lookupEnvValue(ctx, configDB, "LLM_API_KEY"),
		os.Getenv("ANTHROPIC_API_KEY"),
		os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		os.Getenv("LLM_API_KEY"),
		configKey,
	)) != ""
}

func sessionLLMProviderEnvItems(session *Session) []SessionEnvVar {
	if session == nil {
		return nil
	}
	if len(session.ProviderEnvItems) > 0 {
		return session.ProviderEnvItems
	}
	return session.EnvItems
}

func writeCodexLLMConfig(session *Session, model, baseURL, wireAPI string) error {
	if session == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if model == "" || baseURL == "" {
		return nil
	}
	path := filepath.Join(hostSessionHome(session), ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	payload := fmt.Sprintf(`model_provider = "agent_compose"
model = %q

[model_providers.agent_compose]
name = "agent-compose"
base_url = %q
env_key = "AGENT_COMPOSE_SESSION_TOKEN"
wire_api = %q
request_max_retries = 30
stream_max_retries = 50
stream_idle_timeout_ms = 120000

[sandbox_workspace_write]
exclude_tmpdir_env_var = false
exclude_slash_tmp = false
network_access = true

[shell_environment_policy]
inherit = "all"
ignore_default_excludes = false

[history]
persistence = "save-all"
`, model, baseURL, normalizeLLMWireAPI(wireAPI))
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		return fmt.Errorf("write codex config: %w", err)
	}
	return nil
}

func guestRuntimeLLMBaseURL(config *appconfig.Config, session *Session) string {
	if config == nil {
		return ""
	}
	if base := strings.TrimRight(strings.TrimSpace(config.RuntimeBaseURL), "/"); base != "" {
		return base
	}
	listen := strings.TrimSpace(config.HttpListen)
	if listen == "" {
		return ""
	}
	host, port, ok := strings.Cut(listen, ":")
	if !ok {
		return ""
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if session != nil && strings.EqualFold(session.Summary.Driver, "docker") && (host == "127.0.0.1" || host == "localhost") {
		return ""
	}
	return "http://" + host + ":" + port
}

func mergeManagedExecEnv(base map[string]string, managed map[string]string) map[string]string {
	if len(base) == 0 && len(managed) == 0 {
		return nil
	}
	result := make(map[string]string, len(base)+len(managed))
	for key, value := range base {
		if llmProviderKeyName(key) {
			continue
		}
		result[key] = value
	}
	for key, value := range managed {
		result[key] = value
	}
	return result
}

func envItemsFromMap(values map[string]string, secret bool) []SessionEnvVar {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]SessionEnvVar, 0, len(keys))
	for _, key := range keys {
		items = append(items, SessionEnvVar{Name: key, Value: values[key], Secret: secret})
	}
	return items
}
