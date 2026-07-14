package llms

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolveRuntimeLLMFacadeTargetRoutesRequestedModelThroughPinnedProvider(t *testing.T) {
	ctx := context.Background()
	store := &runtimeFacadeTargetStore{
		providers: []Provider{
			{ID: "provider-1", ProviderType: ProviderFamilyOpenAI, DefaultWireAPI: APIProtocolChatCompletions, BaseURL: "https://provider-1.test", AuthHeader: "Authorization", AuthScheme: "Bearer", APIKey: "secret", Enabled: true},
			{ID: "provider-2", ProviderType: ProviderFamilyOpenAI, DefaultWireAPI: APIProtocolResponses, BaseURL: "https://provider-2.test", Enabled: true},
		},
		models: []Model{
			{ID: "configured-id", Name: "configured-name", Enabled: true},
			{ID: "other-provider-id", Name: "other-provider-model", Enabled: true},
		},
		wireAPIs: map[string]string{
			"provider-1\x00configured-id":     APIProtocolResponses,
			"provider-2\x00other-provider-id": APIProtocolResponses,
		},
	}

	tests := []struct {
		name         string
		model        string
		wantWireAPI  string
		wantEndpoint string
	}{
		{name: "configured model keeps provider wire override", model: "configured-name", wantWireAPI: APIProtocolResponses, wantEndpoint: "/v1/responses"},
		{name: "model bound only to another provider stays pinned", model: "other-provider-model", wantWireAPI: APIProtocolChatCompletions, wantEndpoint: "/v1/chat/completions"},
		{name: "unknown model uses provider default", model: "upstream-only-model", wantWireAPI: APIProtocolChatCompletions, wantEndpoint: "/v1/chat/completions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target, err := ResolveRuntimeLLMFacadeTarget(ctx, store, tc.model, "provider-1")
			if err != nil {
				t.Fatalf("ResolveRuntimeLLMFacadeTarget returned error: %v", err)
			}
			if target.Provider.ID != "provider-1" || target.Model.ID != tc.model || target.Model.Name != tc.model {
				t.Fatalf("target identity = %#v", target)
			}
			if target.WireAPI != tc.wantWireAPI || !strings.HasSuffix(target.Endpoint, tc.wantEndpoint) {
				t.Fatalf("target route = %#v, want wire=%q endpoint suffix=%q", target, tc.wantWireAPI, tc.wantEndpoint)
			}
			if target.Headers.Get("Authorization") != "Bearer secret" {
				t.Fatalf("target Authorization = %q", target.Headers.Get("Authorization"))
			}
		})
	}
}

func TestResolveRuntimeLLMFacadeTargetUsesAnthropicProviderForUnknownModel(t *testing.T) {
	store := &runtimeFacadeTargetStore{
		providers: []Provider{{ID: "anthropic-1", ProviderType: ProviderFamilyAnthropic, DefaultWireAPI: APIProtocolMessages, BaseURL: "https://anthropic.test", Enabled: true}},
	}
	target, err := ResolveRuntimeLLMFacadeTarget(context.Background(), store, "upstream-only-model", "anthropic-1")
	if err != nil {
		t.Fatalf("ResolveRuntimeLLMFacadeTarget returned error: %v", err)
	}
	if target.Provider.ID != "anthropic-1" || target.Model.Name != "upstream-only-model" || target.WireAPI != APIProtocolMessages || !strings.HasSuffix(target.Endpoint, "/v1/messages") {
		t.Fatalf("target = %#v", target)
	}
}

func TestResolveRuntimeLLMFacadeTargetFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := &runtimeFacadeTargetStore{
		providers: []Provider{{ID: "provider-1", ProviderType: ProviderFamilyOpenAI, DefaultWireAPI: APIProtocolResponses, BaseURL: "https://provider.test", Enabled: true}},
	}

	for _, tc := range []struct {
		name       string
		model      string
		providerID string
	}{
		{name: "missing model", providerID: "provider-1"},
		{name: "missing token provider", model: "gpt"},
		{name: "unknown token provider", model: "gpt", providerID: "provider-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ResolveRuntimeLLMFacadeTarget(ctx, store, tc.model, tc.providerID); err == nil {
				t.Fatal("ResolveRuntimeLLMFacadeTarget returned nil error")
			}
		})
	}

	store.providersErr = errors.New("provider store failed")
	if _, err := ResolveRuntimeLLMFacadeTarget(ctx, store, "gpt", "provider-1"); !errors.Is(err, store.providersErr) {
		t.Fatalf("provider store error = %v", err)
	}
	store.providersErr = nil
	store.modelsErr = errors.New("model store failed")
	if _, err := ResolveRuntimeLLMFacadeTarget(ctx, store, "gpt", "provider-1"); !errors.Is(err, store.modelsErr) {
		t.Fatalf("model store error = %v", err)
	}
	store.modelsErr = nil
	store.models = []Model{{ID: "gpt", Name: "gpt", Enabled: true}}
	store.wireAPIErr = errors.New("wire api store failed")
	if _, err := ResolveRuntimeLLMFacadeTarget(ctx, store, "gpt", "provider-1"); !errors.Is(err, store.wireAPIErr) {
		t.Fatalf("wire api store error = %v", err)
	}
}

type runtimeFacadeTargetStore struct {
	providers    []Provider
	models       []Model
	wireAPIs     map[string]string
	providersErr error
	modelsErr    error
	wireAPIErr   error
}

func (s *runtimeFacadeTargetStore) ListEnabledLLMProviders(context.Context) ([]Provider, error) {
	return s.providers, s.providersErr
}

func (s *runtimeFacadeTargetStore) ListEnabledLLMModels(context.Context) ([]Model, error) {
	return s.models, s.modelsErr
}

func (s *runtimeFacadeTargetStore) LLMProviderModelWireAPI(_ context.Context, providerID, modelID string) (string, bool, error) {
	if s.wireAPIErr != nil {
		return "", false, s.wireAPIErr
	}
	wireAPI, ok := s.wireAPIs[providerID+"\x00"+modelID]
	return wireAPI, ok, nil
}
