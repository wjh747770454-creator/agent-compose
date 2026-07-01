package agentcompose

import (
	"agent-compose/pkg/agentcompose/llms"
	appconfig "agent-compose/pkg/config"
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/samber/do/v2"
)

type LLMClient struct {
	config   *appconfig.Config
	configDB *ConfigStore
	client   *http.Client
}

type LLMGenerateResult struct {
	Text         string
	Model        string
	ResponseID   string
	FinishReason string
}

const (
	llmAPIProtocolResponses       = llms.APIProtocolResponses
	llmAPIProtocolChatCompletions = llms.APIProtocolChatCompletions
	llmAPIProtocolMessages        = llms.APIProtocolMessages
)

func NewLLMClient(di do.Injector) (*LLMClient, error) {
	config := do.MustInvoke[*appconfig.Config](di)
	return &LLMClient{
		config:   config,
		configDB: do.MustInvoke[*ConfigStore](di),
		client: &http.Client{
			Timeout: config.LLMTimeout,
		},
	}, nil
}

func (c *LLMClient) Generate(ctx context.Context, prompt, model, outputSchemaJSON string) (LLMGenerateResult, error) {
	if c == nil {
		return LLMGenerateResult{}, llmClientUnavailableError()
	}
	target, err := resolveLLMTarget(ctx, c.config, c.configDB, model)
	if err != nil {
		return LLMGenerateResult{}, err
	}
	result, err := llms.Generate(ctx, c.client, llms.GenerateRequest{
		Endpoint:         target.Endpoint,
		Protocol:         target.WireAPI,
		Prompt:           prompt,
		Model:            firstNonEmpty(model, target.Model.Name, target.Model.ID),
		OutputSchemaJSON: outputSchemaJSON,
		Headers:          target.Headers,
	})
	if err != nil {
		return LLMGenerateResult{}, err
	}
	return LLMGenerateResult{
		Text:         result.Text,
		Model:        result.Model,
		ResponseID:   result.ResponseID,
		FinishReason: result.FinishReason,
	}, nil
}

func llmClientUnavailableError() error {
	return &llmClientError{message: "llm client is unavailable"}
}

type llmClientError struct {
	message string
}

func (e *llmClientError) Error() string {
	return e.message
}

func applyLLMForwardHeaders(dst http.Header, src http.Header) {
	llms.ApplyForwardHeaders(dst, src)
}

func (c *LLMClient) resolveSetting(ctx context.Context, fallback string, keys ...string) string {
	if value := strings.TrimSpace(c.lookupGlobalEnv(ctx, keys...)); value != "" {
		return value
	}
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(strings.TrimSpace(key))); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(fallback); value != "" {
		return value
	}
	return ""
}

func (c *LLMClient) resolveEndpoint(ctx context.Context) string {
	return c.resolveEndpointForProtocol(ctx, c.resolveProtocol(ctx))
}

func (c *LLMClient) resolveEndpointForProtocol(ctx context.Context, protocol string) string {
	if value := strings.TrimSpace(c.lookupGlobalEnv(ctx, "LLM_API_ENDPOINT")); value != "" {
		return normalizeLLMAPIEndpointForProtocol(value, protocol)
	}
	if value := strings.TrimSpace(os.Getenv("LLM_API_ENDPOINT")); value != "" {
		return normalizeLLMAPIEndpointForProtocol(value, protocol)
	}
	if c != nil && c.config != nil {
		if value := strings.TrimSpace(c.config.LLMAPIEndpoint); value != "" {
			return normalizeLLMAPIEndpointForProtocol(value, protocol)
		}
	}
	return normalizeLLMAPIEndpointForProtocol("https://api.openai.com", protocol)
}

func (c *LLMClient) resolveProtocol(ctx context.Context) string {
	protocol := strings.ToLower(strings.TrimSpace(c.lookupGlobalEnv(ctx, "LLM_API_PROTOCOL")))
	if protocol == "" {
		protocol = strings.ToLower(strings.TrimSpace(os.Getenv("LLM_API_PROTOCOL")))
	}
	if protocol == "" && c != nil && c.config != nil {
		protocol = strings.ToLower(strings.TrimSpace(c.config.LLMAPIProtocol))
	}
	switch strings.ReplaceAll(protocol, "-", "_") {
	case "", llmAPIProtocolResponses:
		return llmAPIProtocolResponses
	case "chat", "chat_completions", "chat_completion":
		return llmAPIProtocolChatCompletions
	default:
		return protocol
	}
}

func (c *LLMClient) lookupGlobalEnv(ctx context.Context, keys ...string) string {
	if c == nil || c.configDB == nil || len(keys) == 0 {
		return ""
	}
	items, err := c.configDB.ListGlobalEnv(ctx)
	if err != nil {
		return ""
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		for _, item := range items {
			if !strings.EqualFold(strings.TrimSpace(item.Name), key) {
				continue
			}
			if value := strings.TrimSpace(item.Value); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizeLLMAPIEndpoint(raw string) string {
	return llms.NormalizeAPIEndpoint(raw)
}

func normalizeLLMAPIEndpointForProtocol(raw, protocol string) string {
	return llms.NormalizeAPIEndpointForProtocol(raw, protocol)
}
