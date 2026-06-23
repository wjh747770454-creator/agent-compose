import type { Provider } from "./types.js";

export function normalizeProvider(raw: unknown): Provider {
  const provider = String(raw ?? "").trim().toLowerCase();
  if (!provider) {
    throw new Error("provider is required; expected one of: codex, claude, gemini");
  }
  switch (provider) {
    case "codex":
      return "codex";
    case "claude":
    case "claude-code":
    case "claude_code":
      return "claude";
    case "gemini":
    case "gemini-cli":
    case "gemini_cli":
      return "gemini";
    default:
      throw new Error(`unsupported provider ${JSON.stringify(raw)}; expected one of: codex, claude, gemini`);
  }
}
