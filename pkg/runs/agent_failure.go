package runs

import "strings"

func agentExecutionFailedMessage(detail string) string {
	kind := agentFailureKind(detail)
	if kind == "" {
		return "agent execution failed"
	}
	return "agent execution failed: " + kind
}

func agentFailureKind(detail string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(detail), " "))
	switch {
	case normalized == "":
		return ""
	case containsAny(normalized, "context canceled", "cancelled", "canceled"):
		return "cancelled"
	case containsAny(normalized, "deadline exceeded", "timeout", "timed out"):
		return "timeout"
	case containsAny(normalized, "unauthenticated", "unauthorized", "401", "authentication", "invalid api key", "capset token"):
		return "authentication blocked"
	case containsAny(normalized, "permission denied", "forbidden", "403"):
		return "permission blocked"
	case containsAny(normalized, "unavailable", "connection refused", "no such host", "temporary failure", "service unavailable"):
		return "runtime unavailable"
	case containsAny(normalized, "outputschema", "output schema", "json output", "no result payload", "empty stdout"):
		return "result validation failed"
	default:
		return "runtime failed"
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
