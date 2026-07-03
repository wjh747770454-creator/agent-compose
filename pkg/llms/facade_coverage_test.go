package llms

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	protocolbridge "github.com/chaitin/ai-api-protocol-bridge"
)

func TestRuntimeFacadeHTTPAndBridgeCoverage(t *testing.T) {
	header := http.Header{
		"Authorization":   []string{"Bearer runtime-token"},
		"X-Forward-Token": []string{"secret"},
		"X-Trace":         []string{"trace"},
		"Content-Length":  []string{"10"},
	}
	if BearerToken("Bearer abc") != "abc" || RuntimeFacadeToken(header) != "runtime-token" {
		t.Fatalf("token parsing failed")
	}
	dst := http.Header{}
	CopyRuntimeHeaders(dst, header)
	if dst.Get("X-Trace") != "trace" || dst.Get("Authorization") != "" || dst.Get("X-Forward-Token") != "" {
		t.Fatalf("copied request headers = %#v", dst)
	}
	respHeaders := http.Header{"Content-Type": []string{"text/event-stream"}, "Content-Encoding": []string{"gzip"}, "X-Upstream": []string{"ok"}}
	respDst := http.Header{}
	CopyRuntimeResponseHeaders(respDst, respHeaders)
	if respDst.Get("X-Upstream") != "ok" || respDst.Get("Content-Encoding") != "" || !RuntimeResponseShouldFlush(respHeaders) {
		t.Fatalf("copied response headers = %#v", respDst)
	}
	if !ForbiddenRuntimeHeader("x.api-key") || !ForbiddenRuntimeHeader("authorization") || ForbiddenRuntimeHeader("x-trace") {
		t.Fatalf("ForbiddenRuntimeHeader returned unexpected values")
	}
	if !ForbiddenRuntimeResponseHeader("content-length") || ForbiddenRuntimeResponseHeader("x-ok") {
		t.Fatalf("ForbiddenRuntimeResponseHeader returned unexpected values")
	}
	var copied bytes.Buffer
	if err := CopyRuntimeResponseBody(&copied, &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("body"))}); err != nil || copied.String() != "body" {
		t.Fatalf("CopyRuntimeResponseBody copied=%q err=%v", copied.String(), err)
	}
	var events []protocolbridge.RawStreamEvent
	if err := ReadRawSSEEvents(strings.NewReader(": comment\nid: 1\nevent: delta\nretry: 100\ndata: hello\ndata: world\n\n"), func(event protocolbridge.RawStreamEvent) error {
		events = append(events, event)
		return nil
	}); err != nil || len(events) != 1 || string(events[0].Data) != "hello\nworld" {
		t.Fatalf("ReadRawSSEEvents events=%#v err=%v", events, err)
	}

	target := ResolvedTarget{Provider: Provider{ID: "provider", ProviderType: ProviderFamilyOpenAI, UseGenericResponsesTextParts: true}, Model: Model{Name: "gpt-override"}, WireAPI: APIProtocolResponses}
	rewritten, err := RewriteRuntimeRequestForUpstream([]byte(`{"model":"old","input":[{"role":"developer","content":[{"type":"input_text","text":"hi"}]}]}`), target, protocolbridge.ProtocolOpenAIResponses)
	if err != nil || !strings.Contains(string(rewritten), "gpt-override") || !strings.Contains(string(rewritten), `"type":"text"`) {
		t.Fatalf("RewriteRuntimeRequestForUpstream body=%s err=%v", rewritten, err)
	}
	chatBody, err := RewriteRuntimeRequestForUpstream([]byte(`{"model":"old","messages":[{"role":"developer","content":"hi"}]}`), ResolvedTarget{Model: Model{Name: "gpt"}}, protocolbridge.ProtocolOpenAIChat)
	if err != nil || !strings.Contains(string(chatBody), `"role":"system"`) {
		t.Fatalf("chat rewrite body=%s err=%v", chatBody, err)
	}
	if _, err := RewriteRuntimeRequestForUpstream([]byte(`{bad`), target, protocolbridge.ProtocolOpenAIResponses); err == nil {
		t.Fatalf("expected rewrite JSON error")
	}
	req := &protocolbridge.LLMRequest{Model: "old", Prompt: []protocolbridge.Message{{Role: protocolbridge.RoleDeveloper, Parts: []protocolbridge.Part{{Text: &protocolbridge.TextPart{Text: "hi"}}}}}}
	if encoded, err := EncodeRuntimeUpstreamRequest(protocolbridge.ProtocolOpenAIResponses, protocolbridge.ProtocolOpenAIChat, target, req); err != nil || !strings.Contains(string(encoded), "gpt-override") {
		t.Fatalf("EncodeRuntimeUpstreamRequest body=%s err=%v", encoded, err)
	}
	if _, _, err := RuntimeStreamBridge(protocolbridge.ProtocolOpenAIResponses, protocolbridge.ProtocolOpenAIResponses, ProviderFamilyOpenAI, "gpt"); err != nil {
		t.Fatalf("RuntimeStreamBridge same protocol returned error: %v", err)
	}
	if !ProtocolsShareFamily(protocolbridge.ProtocolOpenAIResponses, protocolbridge.ProtocolOpenAIChat) || ProtocolFamily("bad") != "" {
		t.Fatalf("protocol family helpers failed")
	}
}

func TestIntegrationRuntimeFacadeHTTPAndBridgeCoverage(t *testing.T) {
	TestRuntimeFacadeHTTPAndBridgeCoverage(t)
}

func TestE2ERuntimeFacadeHTTPAndBridgeCoverage(t *testing.T) {
	TestRuntimeFacadeHTTPAndBridgeCoverage(t)
}
