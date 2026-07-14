package llms

import "testing"

func TestNewFacadeTokenRequiresProviderAndKeepsDefaultModel(t *testing.T) {
	if _, _, err := NewFacadeToken("sandbox-1", "gpt-default", "", APIProtocolResponses, "test", "run-1"); err == nil {
		t.Fatal("NewFacadeToken returned nil error without provider")
	}
	raw, token, err := NewFacadeToken("sandbox-1", "gpt-default", " provider-1 ", APIProtocolResponses, "test", "run-1")
	if err != nil {
		t.Fatalf("NewFacadeToken returned error: %v", err)
	}
	if raw == "" || token.Model != "gpt-default" || token.ProviderID != "provider-1" {
		t.Fatalf("token = %#v raw_empty=%v", token, raw == "")
	}
}
