package runs

import "strings"

const maxAgentDisplayOutputBytes = 4096

func agentDisplayOutput(output string) string {
	output = strings.ToValidUTF8(output, "\uFFFD")
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return output
	}
	if !looksLikeAgentRuntimeTranscript(trimmed) {
		return output
	}
	lines := strings.Split(trimmed, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			if len(kept) > 0 && kept[len(kept)-1] != "" {
				kept = append(kept, "")
			}
			continue
		}
		candidate = sanitizeAgentDisplayOutputLine(candidate)
		if candidate == "" {
			continue
		}
		if shouldDropAgentRuntimeTranscriptLine(candidate) {
			continue
		}
		kept = append(kept, candidate)
	}
	if hasCJKLine(kept) {
		kept = keepCJKLines(kept)
	}
	shaped := strings.TrimSpace(strings.Join(compactBlankLines(kept), "\n"))
	if shaped == "" {
		return truncateAgentDisplayOutput(trimmed)
	}
	return truncateAgentDisplayOutput(shaped)
}

func looksLikeAgentRuntimeTranscript(output string) bool {
	return strings.Contains(output, "\n$ ") ||
		strings.Contains(output, "/data/runtime/mpi/catalog.md") ||
		strings.Contains(output, "x-octobus-capset") ||
		strings.Contains(output, "agent-compose-runtime-sdk") ||
		strings.Contains(output, "ResolveRecentReference") ||
		strings.Contains(output, "读取执行规范") ||
		strings.Contains(output, "执行规范") ||
		strings.Contains(output, "\nPROJECT ") ||
		looksLikeLeakedSkillDocument(output)
}

func sanitizeAgentDisplayOutputLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(line, "{")
	line = strings.TrimSpace(line)
	for _, replacement := range []struct {
		old string
		new string
	}{
		{"先读取执行规范，再", ""},
		{"先读取执行规范，", ""},
		{"读取执行规范，再", ""},
		{"读取执行规范，", ""},
		{"读取执行规范", ""},
		{"执行规范", ""},
	} {
		line = strings.ReplaceAll(line, replacement.old, replacement.new)
	}
	return strings.TrimSpace(line)
}

func shouldDropAgentRuntimeTranscriptLine(line string) bool {
	lower := strings.ToLower(line)
	switch {
	case strings.HasPrefix(line, "$ "):
		return true
	case strings.HasPrefix(line, "#"):
		return true
	case strings.HasPrefix(line, "/"):
		return true
	case strings.HasPrefix(line, "|"):
		return true
	case strings.HasPrefix(line, "{") || strings.HasPrefix(line, "}") || strings.HasPrefix(line, "\""):
		return true
	case line == "]" || line == ");" || line == ")," || line == "};":
		return true
	case strings.HasSuffix(line, ",") && (!containsCJK(line) || looksLikeCodeFragment(line)):
		return true
	case strings.HasPrefix(line, "package/"):
		return true
	case strings.HasPrefix(line, "CAP_GRPC_") || strings.HasPrefix(line, "OPENAI_") || strings.HasPrefix(line, "LLM_"):
		return true
	case strings.HasPrefix(line, "- grpc:") || strings.HasPrefix(line, "- also send"):
		return true
	case strings.HasPrefix(line, "- endpoint:") || strings.HasPrefix(line, "- on every call") || strings.HasPrefix(line, "- schemas can be discovered"):
		return true
	case strings.Contains(line, "CAP_TOKEN") || strings.Contains(line, "x-capability-sandbox-token"):
		return true
	case strings.Contains(line, "/data/runtime/mpi/"):
		return true
	case strings.Contains(line, "/root/.agents/"):
		return true
	case strings.Contains(line, "x-octobus-capset"):
		return true
	case strings.Contains(lower, "capability gateway access") || strings.Contains(lower, "capabilities are reachable"):
		return true
	case strings.Contains(lower, "use this skill when"):
		return true
	case strings.Contains(lower, "complete the user's actual business objective"):
		return true
	case strings.Contains(lower, "never expose internal command lines"):
		return true
	case strings.Contains(lower, "office-execution"):
		return true
	case strings.Contains(line, "写入门禁") || strings.Contains(line, "本文件规则"):
		return true
	case strings.Contains(line, "当写入对象来自") && strings.Contains(line, "近期指代"):
		return true
	case strings.Contains(line, "内部规则") && strings.Contains(line, "Skill"):
		return true
	case strings.HasPrefix(line, "PROJECT "):
		return true
	case strings.Contains(line, "ResolveRecentReference"):
		return true
	case strings.Contains(lower, "runtime-grounded") || strings.Contains(lower, "do not emulate a missing ledger"):
		return true
	case strings.Contains(lower, "when the user's phrase contains discriminating business words"):
		return true
	case strings.Contains(lower, "pass them as `keywords`") || strings.Contains(lower, "keywords are deterministic filters"):
		return true
	case strings.Contains(lower, "exact-count certainty rule"):
		return true
	case strings.Contains(lower, "metadata shown in the table"):
		return true
	case strings.Contains(line, ".v1.") && strings.Contains(line, "Service"):
		return true
	case strings.Contains(line, "Chaitin_CRM") || strings.Contains(line, "AITableService"):
		return true
	case strings.Contains(lower, "agent-compose-runtime-sdk"):
		return true
	case strings.HasPrefix(lower, "import ") || strings.HasPrefix(lower, "export ") || strings.HasPrefix(lower, "const "):
		return true
	case strings.HasPrefix(lower, "func ") || strings.HasPrefix(lower, "if ") || strings.HasPrefix(lower, "for "):
		return true
	case strings.HasPrefix(lower, "return ") || strings.HasPrefix(lower, "process.") || strings.HasPrefix(lower, "throw "):
		return true
	case looksLikeJavaScriptObjectLine(line):
		return true
	default:
		return false
	}
}

func looksLikeLeakedSkillDocument(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "use this skill when") ||
		strings.Contains(lower, "complete the user's actual business objective") ||
		strings.Contains(lower, "never expose internal command lines") ||
		strings.Contains(lower, "office-execution") ||
		strings.Contains(output, "写入门禁") ||
		strings.Contains(output, "本文件规则") ||
		(strings.Contains(output, "当写入对象来自") && strings.Contains(output, "近期指代")) ||
		strings.Contains(output, "ResolveRecentReference") ||
		strings.Contains(lower, "do not emulate a missing ledger") ||
		strings.Contains(lower, "when the user's phrase contains discriminating business words") ||
		strings.Contains(lower, "keywords are deterministic filters") ||
		strings.Contains(lower, "exact-count certainty rule")
}

func looksLikeCodeFragment(line string) bool {
	for _, token := range []string{"=>", "(", ")", "{", "}", ":", "JSON.", "sha256", "prompt", "result", "schema", "attempt", "delegate"} {
		if strings.Contains(line, token) {
			return true
		}
	}
	return false
}

func looksLikeJavaScriptObjectLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.ContainsAny(line, "：。；，、") {
		return false
	}
	for _, token := range []string{": ", "=>", "json.", "structuredresult", "unexpected", "referenceerrors", "maxattempts", "timeoutms", "outputschema", "onretry", "nextprompt", "sourceprompt", "wrapperattempt"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func containsCJK(line string) bool {
	for _, r := range line {
		if (r >= '\u4E00' && r <= '\u9FFF') || (r >= '\u3400' && r <= '\u4DBF') {
			return true
		}
	}
	return false
}

func hasCJKLine(lines []string) bool {
	for _, line := range lines {
		if containsCJK(line) {
			return true
		}
	}
	return false
}

func keepCJKLines(lines []string) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" || containsCJK(line) {
			kept = append(kept, line)
		}
	}
	return kept
}

func compactBlankLines(lines []string) []string {
	compact := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" && (len(compact) == 0 || compact[len(compact)-1] == "") {
			continue
		}
		compact = append(compact, line)
	}
	for len(compact) > 0 && compact[len(compact)-1] == "" {
		compact = compact[:len(compact)-1]
	}
	return compact
}

func truncateAgentDisplayOutput(output string) string {
	if len(output) <= maxAgentDisplayOutputBytes {
		return strings.ToValidUTF8(output, "\uFFFD")
	}
	headroom := maxAgentDisplayOutputBytes - len("\n\n[output truncated; full transcript is available in run logs]")
	if headroom < 0 {
		headroom = maxAgentDisplayOutputBytes
	}
	var builder strings.Builder
	for _, r := range output {
		if builder.Len()+len(string(r)) > headroom {
			break
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String()) + "\n\n[output truncated; full transcript is available in run logs]"
}
