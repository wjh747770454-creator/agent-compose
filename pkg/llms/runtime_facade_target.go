package llms

import (
	"context"
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"
)

type runtimeLLMFacadeTargetStore interface {
	ProviderListStore
	ProviderModelWireAPIStore
	ListEnabledLLMModels(ctx context.Context) ([]Model, error)
}

// ResolveRuntimeLLMFacadeTarget pins a runtime request to the provider granted
// by its facade token. The requested model is not an authorization boundary:
// unknown models use the provider's default wire API and are left for the
// upstream provider to accept or reject.
func ResolveRuntimeLLMFacadeTarget(ctx context.Context, store runtimeLLMFacadeTargetStore, requestedModel, providerID string) (ResolvedTarget, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ResolvedTarget{}, domain.ClassifyError(domain.ErrRequired, "llm model is required", nil)
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ResolvedTarget{}, domain.ClassifyError(domain.ErrFailedPrecondition, "llm facade token provider is required", nil)
	}

	providers, err := store.ListEnabledLLMProviders(ctx)
	if err != nil {
		return ResolvedTarget{}, fmt.Errorf("list enabled llm providers for runtime facade: %w", err)
	}
	var provider Provider
	for _, candidate := range providers {
		if candidate.ID == providerID {
			provider = candidate
			break
		}
	}
	if provider.ID == "" {
		return ResolvedTarget{}, domain.ClassifyError(domain.ErrFailedPrecondition, fmt.Sprintf("llm provider %q is not configured", providerID), nil)
	}

	wireAPI := NormalizeWireAPI(provider.DefaultWireAPI)
	models, err := store.ListEnabledLLMModels(ctx)
	if err != nil {
		return ResolvedTarget{}, fmt.Errorf("list enabled llm models for runtime facade: %w", err)
	}
	if configuredModel := SelectModel(models, requestedModel); configuredModel.ID != "" {
		configuredWireAPI, ok, err := store.LLMProviderModelWireAPI(ctx, provider.ID, configuredModel.ID)
		if err != nil {
			return ResolvedTarget{}, fmt.Errorf("resolve runtime facade provider model wire api: %w", err)
		}
		if ok && strings.TrimSpace(configuredWireAPI) != "" {
			wireAPI = NormalizeWireAPI(configuredWireAPI)
		}
	}

	headers, err := ProviderForwardHeaders(provider)
	if err != nil {
		return ResolvedTarget{}, err
	}
	return ResolvedTarget{
		Provider: provider,
		Model:    Model{ID: requestedModel, Name: requestedModel, Enabled: true},
		WireAPI:  wireAPI,
		Endpoint: EndpointForProvider(provider, wireAPI),
		Headers:  headers,
	}, nil
}
