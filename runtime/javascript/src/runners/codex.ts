import { resolveCodexPath } from "../codex-path.js";
import { stringEnv } from "../env.js";
import { uniqueDirectories } from "../paths.js";
import { readStoredThread, writeStoredThread } from "../session-state.js";
import { appendDelta, TranscriptWriter, type TextWriter } from "../transcript.js";
import type { AgentResult, RunnerOptions } from "../types.js";

interface CodexItemState {
  commandStarted?: boolean;
  commandOutput?: string;
  fileChangeEmitted?: boolean;
  mcpStarted?: boolean;
  mcpResultEmitted?: boolean;
  mcpErrorEmitted?: boolean;
  webSearchEmitted?: boolean;
}

interface TranscriptRecorder extends TextWriter {
  transcript(): string;
}

function webSearchQuery(item: Record<string, unknown>): string {
  if (typeof item.query === "string" && item.query.trim()) {
    return item.query;
  }
  const action = item.action as Record<string, unknown> | undefined;
  if (typeof action?.query === "string" && action.query.trim()) {
    return action.query;
  }
  if (Array.isArray(action?.queries)) {
    return action.queries.filter((entry) => typeof entry === "string" && entry.trim()).join(", ");
  }
  return "";
}

export class CodexRunner {
  private readonly itemState = new Map<string, string | CodexItemState>();

  constructor(
    private readonly options: RunnerOptions,
    private readonly writer: TranscriptRecorder = new TranscriptWriter(),
  ) {}

  transcript(): string {
    return this.writer.transcript();
  }

  threadOptions(): Record<string, unknown> {
    return {
      workingDirectory: this.options.workspace,
      additionalDirectories: uniqueDirectories([this.options.stateRoot, this.options.home, this.options.runtimeRoot]),
      skipGitRepoCheck: true,
      sandboxMode: "danger-full-access",
      approvalPolicy: "never",
      networkAccessEnabled: true,
    };
  }

  emitCommand(item: Record<string, unknown> & { id: string }): void {
    const state = (this.itemState.get(item.id) || {}) as CodexItemState;
    state.commandStarted = true;
    state.commandOutput = String(item.aggregated_output || "");
    this.itemState.set(item.id, state);
  }

  emitFileChange(item: Record<string, unknown> & { id: string }): void {
    const changes = Array.isArray(item.changes) ? item.changes : [];
    if (changes.length === 0) {
      return;
    }
    const state = (this.itemState.get(item.id) || {}) as CodexItemState;
    state.fileChangeEmitted = true;
    this.itemState.set(item.id, state);
  }

  emitMcp(item: Record<string, unknown> & { id: string }): void {
    const state = (this.itemState.get(item.id) || {}) as CodexItemState;
    state.mcpStarted = true;
    state.mcpResultEmitted = item.status === "completed" || state.mcpResultEmitted;
    state.mcpErrorEmitted = item.status === "failed" || state.mcpErrorEmitted;
    this.itemState.set(item.id, state);
  }

  emitTodo(item: Record<string, unknown> & { id: string }): void {
    const lines = Array.isArray(item.items)
      ? item.items.map((entry) => {
        const record = entry as Record<string, unknown>;
        return `${record.completed ? "[x]" : "[ ]"} ${record.text}`;
      })
      : [];
    const nextText = lines.length > 0 ? `\n[todo]\n${lines.join("\n")}\n` : "";
    this.itemState.set(item.id, nextText);
  }

  emitWebSearch(item: Record<string, unknown> & { id: string }, eventType: unknown): void {
    const state = (this.itemState.get(item.id) || {}) as CodexItemState;
    if (state.webSearchEmitted) {
      return;
    }
    const query = webSearchQuery(item);
    // Match Codex CLI transcript behavior: if the search completes without an
    // exposed query, still emit the marker so the tool use remains visible.
    if (!query && eventType !== "item.completed") {
      return;
    }
    state.webSearchEmitted = true;
    this.itemState.set(item.id, state);
  }

  handleEvent(event: Record<string, unknown>, result: AgentResult): void {
    if (event.type === "thread.started") {
      result.threadId = String(event.thread_id || result.threadId);
      return;
    }
    if (event.type === "turn.failed") {
      const error = event.error as Record<string, unknown> | undefined;
      const message = String(error?.message || "codex turn failed");
      if (result.finalText.trim()) {
        result.stopReason = "completed_with_runtime_warning";
        result.stderr = message;
        return;
      }
      throw new Error(message);
    }
    if (!event.item || typeof event.item !== "object") {
      return;
    }
    const item = event.item as Record<string, unknown> & { id: string; type: string };
    switch (item.type) {
      case "agent_message":
        appendDelta(this.writer, this.itemState as Map<string, string>, item.id, String(item.text || ""));
        if (event.type === "item.completed") {
          result.finalText = String(item.text || result.finalText);
        }
        break;
      case "reasoning":
        this.itemState.set(item.id, String(item.text || ""));
        break;
      case "command_execution":
        this.emitCommand(item);
        break;
      case "file_change":
        this.emitFileChange(item);
        break;
      case "mcp_tool_call":
        this.emitMcp(item);
        break;
      case "web_search":
        this.emitWebSearch(item, event.type);
        break;
      case "todo_list":
        this.emitTodo(item);
        break;
      case "error":
        this.itemState.set(item.id, String(item.message || "codex item error"));
        break;
      default:
        break;
    }
  }

  async runPrompt(promptText: string): Promise<AgentResult> {
    const { Codex } = await import("@openai/codex-sdk");
    const stored = await readStoredThread(this.options.stateRoot, "codex");
    const codex = new Codex({
      codexPathOverride: resolveCodexPath(),
      env: stringEnv(),
      // `config` (the `--config key=value` overrides) is a CodexOptions field on the
      // constructor; it is NOT read from ThreadOptions/startThread. Injecting the combined
      // Agent Identity + MPI system context here applies to both start and resume flows.
      ...(this.options.systemContext
        ? { config: { developer_instructions: this.options.systemContext } }
        : {}),
    });
    const thread = stored?.threadId
      ? codex.resumeThread(stored.threadId, this.threadOptions())
      : codex.startThread(this.threadOptions());

    const result: AgentResult = {
      provider: "codex",
      threadId: stored?.threadId || "",
      stopReason: "completed",
      finalText: "",
      transcript: "",
      stderr: "",
    };

    const { events } = await thread.runStreamed(
      promptText,
      this.options.outputSchema ? { outputSchema: this.options.outputSchema } : undefined,
    );
    for await (const event of events) {
      this.handleEvent(event as Record<string, unknown>, result);
    }
    result.threadId = thread.id || result.threadId;
    result.transcript = this.writer.transcript();
    if (!result.finalText && result.transcript) {
      result.finalText = result.transcript;
    }
    await writeStoredThread(this.options.stateRoot, "codex", result.threadId);
    return result;
  }
}
