package proxy

import (
	"net/http"
	"strings"
)

const RuntimeLLMFacadePrefix = "/api/runtime/sessions/"

func IsRuntimeLLMFacadeRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	path := r.URL.Path
	if !strings.HasPrefix(path, RuntimeLLMFacadePrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, RuntimeLLMFacadePrefix), "/")
	if len(parts) < 5 || parts[0] == "" || parts[1] != "llm" {
		return false
	}
	switch {
	case len(parts) == 5 && parts[2] == "openai" && parts[3] == "v1" && parts[4] == "responses":
		return true
	case len(parts) == 6 && parts[2] == "openai" && parts[3] == "v1" && parts[4] == "chat" && parts[5] == "completions":
		return true
	case len(parts) == 5 && parts[2] == "anthropic" && parts[3] == "v1" && parts[4] == "messages":
		return true
	default:
		return false
	}
}
