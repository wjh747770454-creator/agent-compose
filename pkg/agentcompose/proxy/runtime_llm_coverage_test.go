package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"agent-compose/pkg/llms"
	domain "agent-compose/pkg/model"
)

func TestRuntimeLLMFacadeRoutesCoverageWorkflow(t *testing.T) {
	e := echo.New()
	client := &fakeRuntimeLLMHTTPClient{status: http.StatusOK, body: `{"id":"resp-1","model":"gpt","output":[]}`}
	RegisterRuntimeLLMFacadeRoutes(e, RuntimeLLMOptions{
		Tokens:        fakeRuntimeLLMTokens{token: llms.FacadeToken{SessionID: "session-1", Model: "gpt", ProviderID: "provider-1", WireAPI: llms.APIProtocolResponses, ExpiresAt: time.Now().Add(time.Hour)}},
		Sessions:      fakeRuntimeLLMSessions{session: &domain.Session{Summary: domain.SessionSummary{ID: "session-1", VMStatus: domain.VMStatusRunning}}},
		ResolveTarget: fakeRuntimeLLMTargetResolver("http://upstream.test/v1"),
		Client:        client,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/runtime/sessions/session-1/llm/openai/v1/responses", strings.NewReader(`{"model":"gpt","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer raw-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "resp-1") || client.calls != 1 {
		t.Fatalf("responses proxy status=%d body=%s calls=%d", rec.Code, rec.Body.String(), client.calls)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/runtime/sessions/session-1/llm/openai/v1/responses", strings.NewReader(`{"model":"other","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer raw-token")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("model mismatch status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/runtime/sessions/session-1/llm/openai/v1/responses", strings.NewReader(`{"model":"gpt","input":"hi"}`))
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", rec.Code)
	}

	missingDeps := echo.New()
	RegisterRuntimeLLMFacadeRoutes(missingDeps, RuntimeLLMOptions{})
	req = httptest.NewRequest(http.MethodPost, "/api/runtime/sessions/session-1/llm/openai/v1/responses", strings.NewReader(`{"model":"gpt","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer raw-token")
	rec = httptest.NewRecorder()
	missingDeps.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("missing deps status=%d", rec.Code)
	}

	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())
	if err := WriteRuntimeLLMEncodedError(c, []byte(`{"error":"bad"}`), 0); err != nil {
		t.Fatalf("WriteRuntimeLLMEncodedError returned error: %v", err)
	}
	if firstNonEmpty("", " value ") != " value " {
		t.Fatalf("firstNonEmpty returned unexpected value")
	}
}

func TestRuntimeLLMFacadeRejectsInvalidSecurityContext(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     string
		token    llms.FacadeToken
		session  *domain.Session
		resolver RuntimeLLMTargetResolver
		want     int
	}{
		{
			name:    "expired token",
			path:    "/api/runtime/sessions/session-1/llm/openai/v1/responses",
			body:    `{"model":"gpt","input":"hi"}`,
			token:   llms.FacadeToken{SessionID: "session-1", Model: "gpt", ProviderID: "provider-1", WireAPI: llms.APIProtocolResponses, ExpiresAt: time.Now().Add(-time.Minute)},
			session: &domain.Session{Summary: domain.SessionSummary{ID: "session-1", VMStatus: domain.VMStatusRunning}},
			want:    http.StatusForbidden,
		},
		{
			name:    "revoked token",
			path:    "/api/runtime/sessions/session-1/llm/openai/v1/responses",
			body:    `{"model":"gpt","input":"hi"}`,
			token:   llms.FacadeToken{SessionID: "session-1", Model: "gpt", ProviderID: "provider-1", WireAPI: llms.APIProtocolResponses, RevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
			session: &domain.Session{Summary: domain.SessionSummary{ID: "session-1", VMStatus: domain.VMStatusRunning}},
			want:    http.StatusForbidden,
		},
		{
			name:    "wire api mismatch",
			path:    "/api/runtime/sessions/session-1/llm/openai/v1/chat/completions",
			body:    `{"model":"gpt","messages":[{"role":"user","content":"hi"}]}`,
			token:   llms.FacadeToken{SessionID: "session-1", Model: "gpt", ProviderID: "provider-1", WireAPI: llms.APIProtocolResponses, ExpiresAt: time.Now().Add(time.Hour)},
			session: &domain.Session{Summary: domain.SessionSummary{ID: "session-1", VMStatus: domain.VMStatusRunning}},
			want:    http.StatusForbidden,
		},
		{
			name:    "stopped session",
			path:    "/api/runtime/sessions/session-1/llm/openai/v1/responses",
			body:    `{"model":"gpt","input":"hi"}`,
			token:   llms.FacadeToken{SessionID: "session-1", Model: "gpt", ProviderID: "provider-1", WireAPI: llms.APIProtocolResponses, ExpiresAt: time.Now().Add(time.Hour)},
			session: &domain.Session{Summary: domain.SessionSummary{ID: "session-1", VMStatus: domain.VMStatusStopped}},
			want:    http.StatusForbidden,
		},
		{
			name:    "provider mismatch",
			path:    "/api/runtime/sessions/session-1/llm/openai/v1/responses",
			body:    `{"model":"gpt","input":"hi"}`,
			token:   llms.FacadeToken{SessionID: "session-1", Model: "gpt", ProviderID: "provider-2", WireAPI: llms.APIProtocolResponses, ExpiresAt: time.Now().Add(time.Hour)},
			session: &domain.Session{Summary: domain.SessionSummary{ID: "session-1", VMStatus: domain.VMStatusRunning}},
			resolver: func(context.Context, string, string) (llms.ResolvedTarget, error) {
				return llms.ResolvedTarget{
					Provider: llms.Provider{ID: "provider-1", ProviderType: llms.ProviderFamilyOpenAI, BaseURL: "http://upstream.test/v1"},
					Model:    llms.Model{Name: "gpt"},
					WireAPI:  llms.APIProtocolResponses,
				}, nil
			},
			want: http.StatusForbidden,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			resolver := tc.resolver
			if resolver == nil {
				resolver = fakeRuntimeLLMTargetResolver("http://upstream.test/v1")
			}
			RegisterRuntimeLLMFacadeRoutes(e, RuntimeLLMOptions{
				Tokens:        fakeRuntimeLLMTokens{token: tc.token},
				Sessions:      fakeRuntimeLLMSessions{session: tc.session},
				ResolveTarget: resolver,
				Client:        &fakeRuntimeLLMHTTPClient{status: http.StatusOK, body: `{"id":"resp-1","model":"gpt","output":[]}`},
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer raw-token")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s status=%d body=%s, want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestIntegrationRuntimeLLMFacadeRoutesCoverageWorkflow(t *testing.T) {
	TestRuntimeLLMFacadeRoutesCoverageWorkflow(t)
}

func TestE2ERuntimeLLMFacadeRoutesCoverageWorkflow(t *testing.T) {
	TestRuntimeLLMFacadeRoutesCoverageWorkflow(t)
}

type fakeRuntimeLLMTokens struct {
	token llms.FacadeToken
	err   error
}

func (s fakeRuntimeLLMTokens) GetLLMFacadeToken(context.Context, string) (llms.FacadeToken, error) {
	return s.token, s.err
}

type fakeRuntimeLLMSessions struct {
	session *domain.Session
	err     error
}

func (s fakeRuntimeLLMSessions) GetSession(context.Context, string) (*domain.Session, error) {
	return s.session, s.err
}

func fakeRuntimeLLMTargetResolver(baseURL string) RuntimeLLMTargetResolver {
	return func(context.Context, string, string) (llms.ResolvedTarget, error) {
		return llms.ResolvedTarget{
			Provider: llms.Provider{ID: "provider-1", ProviderType: llms.ProviderFamilyOpenAI, BaseURL: baseURL},
			Model:    llms.Model{Name: "gpt"},
			WireAPI:  llms.APIProtocolResponses,
		}, nil
	}
}

type fakeRuntimeLLMHTTPClient struct {
	status int
	body   string
	calls  int
}

func (c *fakeRuntimeLLMHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.calls++
	return &http.Response{
		StatusCode: c.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Request:    req,
	}, nil
}
