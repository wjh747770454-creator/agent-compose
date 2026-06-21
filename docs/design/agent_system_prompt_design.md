# Agent System Prompt — Phase 1 Design

**Status:** Phase 1 is **implemented** in the current codebase. Sections through
[Success Criteria (Phase 1, verified)](#success-criteria-phase-1-verified) document what shipped in
this change. [Next Steps](#next-steps) lists work that was **not** part of Phase 1.

Chinese version: [../zh-CN/design/agent_system_prompt_design.md](../zh-CN/design/agent_system_prompt_design.md)

Related documents:

- Runtime invocation contract: [agent-compose-runtime-js_contract.md](agent-compose-runtime-js_contract.md)

Before Phase 1, `AgentDefinition.system_prompt` was persisted, exposed through
API/Proto, and editable in the Agents UI, but the execution path never read it.
Only the MPI (Model Program Interface) capability catalog reached provider
system/developer instruction channels.

Phase 1 closed that gap by wiring agent identity into a layered prompt model
without introducing a full platform runtime brief.

## Background

### What already existed

| Layer | Storage / source | Runtime behavior (pre–Phase 1) |
| --- | --- | --- |
| Agent identity | `AgentDefinition.system_prompt` in `config_store` | **Not injected** |
| MPI catalog | Host writes `runtime/mpi/catalog.md` from OctoBus capsets | Injected into Codex / Claude only |
| Per-turn task | Host writes `state/agents/prompts/<provider>-<nano>.txt` | Passed via `--message-file` |

Provider injection before Phase 1:

| Provider | Mechanism |
| --- | --- |
| Codex | `config.developer_instructions = mpiContext` |
| Claude | `systemPrompt: { preset: "claude_code", append: mpiContext }` |
| Gemini | No system context (MPI ignored) |

### Prompt model

agent-compose treats runtime instructions as three separable layers:

1. **Platform context** — MPI capability catalog (existing)
2. **Agent identity** — per-agent `system_prompt`
3. **Per-turn task** — user message for the current turn

Phase 1 wired **Agent Identity** into the existing MPI platform layer. It did
not add a deployment-wide runtime brief, file-based workspace injection, or
skills discovery.

## Goals and Non-Goals

### Phase 1 scope (delivered)

- Make configured `system_prompt` affect Codex, Claude, and Gemini runs
- Preserve per-turn message isolation (`--message-file` carries task text only)
- Compose Agent Identity **before** the MPI catalog when both are present
- Remain backward compatible when `system_prompt` is empty or no agent binding exists
- Pass live combined context on Codex resume (via constructor-level config)
- Cover loader runs and managed project runs that bind an agent definition

### Deferred (see [Next Steps](#next-steps))

- Renaming `system_prompt` → `instructions`
- Workspace-level global context field
- AGENTS.md / CLAUDE.md marker-block file injection and cleanup
- Skills list or skill-bound prompt sections
- Platform-wide issue workflow brief (mentions, metadata semantics, comment formatting)
- Frontend changes (UI already supports editing `system_prompt`)
- Proto or DB schema changes (`system_prompt` column already exists)
- Injecting `description` into runtime instructions

## Prompt Layering

Implemented composition for provider system / developer instructions:

```text
┌──────────────────────────────────────────────────────────────┐
│ Provider system / developer instructions                     │
│  ┌────────────────────────────────────────────────────────┐  │
│  │ ## Agent Identity                                      │  │
│  │ AgentDefinition.system_prompt (DB, per agent)          │  │
│  ├────────────────────────────────────────────────────────┤  │
│  │ ## MPI Catalog                                         │  │
│  │ OctoBus capset guides → runtime/mpi/catalog.md         │  │
│  └────────────────────────────────────────────────────────┘  │
├──────────────────────────────────────────────────────────────┤
│ Per-turn user message (--message-file)                       │
│ Chat text, loader script prompt, structured task input       │
└──────────────────────────────────────────────────────────────┘
```

Rules:

- Agent Identity is omitted when `system_prompt` is empty after trim.
- MPI is omitted when no readable MPI catalog exists.
- When MPI is present, the runtime preserves the MPI string from
  `formatMpiContext`, including the existing `## MPI Catalog` header.
- `description` is catalog metadata only and MUST NOT appear in runtime instructions.

## End-to-End Data Flow

```text
┌─────────────┐     claim / chat      ┌──────────────────┐
│ ConfigStore │◄──GetAgentDefinition──│ Service/Executor │
│ system_prompt│                      │                  │
└─────────────┘                       │ writeAgent       │
                                      │ SystemPromptFile │
                                      │ writeAgent       │
                                      │ PromptFile       │
                                      └────────┬─────────┘
                                               │ ExecStream
                                               ▼
                              ┌────────────────────────────────┐
                              │ guest: agent-compose-runtime   │
                              │   prompt                       │
                              │   --provider codex|claude|gemini│
                              │   --message-file …/prompts/…   │
                              │   --system-prompt-file …/system-prompts/… │
                              │   --state-root /data/state     │
                              └────────┬───────────────────────┘
                                       │
                    readSystemPromptFile + readMpiContext
                                       │
                              buildSystemContext()
                                       │
              ┌────────────────────────┼────────────────────────┐
              ▼                        ▼                        ▼
         CodexRunner              ClaudeRunner            GeminiRunner
    developer_instructions    systemPrompt.append      prepend to -p
```

Guest command shape (when system prompt is non-empty):

```sh
agent-compose-runtime prompt \
  --provider <provider> \
  --message-file /data/state/agents/prompts/<provider>-<unix_nano>.txt \
  --system-prompt-file /data/state/agents/system-prompts/<provider>-<unix_nano>.txt \
  --state-root /data/state \
  --workspace /workspace \
  --home /root
```

When `system_prompt` is empty, `--system-prompt-file` is omitted entirely.

## Host (Go) Implementation

Primary files: `pkg/agentcompose/service.go`, `pkg/agentcompose/exec.go`,
`pkg/agentcompose/loader_manager.go`, `pkg/agentcompose/run_service.go`.

### Resolve agent system prompt

**Function:** `Executor.resolveAgentSystemPrompt(ctx, session, agentDefinitionID string) (string, error)`

Resolution order:

1. If `agentDefinitionID` (`ExecuteAgentRequest.AgentDefinitionID`) is non-empty,
   load that agent definition directly.
2. Else, if the session has tags `source=agent` and `agent_id=<uuid>`, load by
   tagged agent id.
3. Else return `""` (not an error).

On DB lookup failure, the host logs a warning and runs without agent identity
(MPI-only behavior). Runs are not failed for a missing definition row.

### Write system prompt file

**Function:** `writeAgentSystemPromptFile(config, session, provider, systemPrompt string) (string, error)`

| Property | Value |
| --- | --- |
| Host directory | `{hostSessionDir}/state/agents/system-prompts/` |
| Filename | `{normalizeAgentKind(provider)}-{unix_nano}.txt` |
| Guest path | `{GuestStateRoot}/agents/system-prompts/{same name}` |
| Content | UTF-8 raw `systemPrompt` bytes (section headers added by guest runtime) |
| Empty prompt | Return `""`; do not create a file |

This mirrors `writeAgentPromptFile` for per-turn messages.

### Extend execution request and exec spec

`ExecuteAgentRequest` gains `AgentDefinitionID string`:

- **Loader runs** (`loader_manager.go`): set from resolved agent definition id
  or `loader.Summary.AgentID` fallback.
- **Managed project runs** (`run_service.go`): set from `run.ManagedAgentID`.
- **Session chat runs**: rely on session tags when no explicit id is passed.

`buildAgentExecSpec` accepts an optional `systemPromptPath` and appends
`--system-prompt-file` only when non-empty.

### Backward compatibility retry

If the guest runtime does not recognize `--system-prompt-file` (stderr contains
`unknown option` or `unknown flag` for that flag), the host logs a warning and
retries once without the flag. This allows older guest images to keep running
(MPI-only) until the runtime JS is upgraded.

### Error handling summary

| Condition | Behavior |
| --- | --- |
| Agent id present but DB row missing | Warn; run without agent identity |
| Write system prompt file fails | Fail run (same as prompt file write failure) |
| Empty system prompt | Omit `--system-prompt-file` |
| Old guest runtime | Retry without system prompt file |

## Guest Runtime (TypeScript) Implementation

Primary files: `runtime/javascript/src/system-context.ts`, `prompt.ts`, `cli.ts`,
`types.ts`, and provider runners under `runners/`.

### New module: `system-context.ts`

```typescript
buildSystemContext(agentPrompt: string, mpiContext: string): string
readSystemPromptFile(path?: string): Promise<string>
```

Composition logic:

- When agent prompt is non-empty: emit `## Agent Identity`, blank line, trimmed
  agent text.
- When MPI is non-empty and agent prompt is also non-empty: append the trimmed
  MPI context unchanged, preserving its `## MPI Catalog` header.
- When agent prompt is empty but MPI exists: return MPI unchanged for backward
  compatibility.
- When both are empty: return `""`.

`readSystemPromptFile` returns `""` for missing path, `ENOENT`, or empty file
content after trim.

### CLI and prompt command

- `cli.ts`: `prompt` subcommand adds optional `--system-prompt-file <path>`.
- `prompt.ts`: reads the file, reads MPI catalog, calls `buildSystemContext`,
  passes the result to runners as `systemContext`.

### RunnerOptions change

`RunnerOptions.mpiContext` is replaced by `systemContext` (the combined string).
MPI is still read internally in `prompt.ts` for composition; runners no longer
receive raw MPI alone.

## Provider-Specific Injection

### Codex

Combined context is passed on the Codex constructor:

```typescript
new Codex({
  config: { developer_instructions: systemContext },
})
```

The `@openai/codex-sdk` reads `config` at constructor scope, not from
`ThreadOptions`. That means both `startThread` and `resumeThread` receive the
current combined context on each run, including after `system_prompt` edits.

### Claude

Combined context is appended to the Claude Code preset:

```typescript
systemPrompt: {
  type: "preset",
  preset: "claude_code",
  append: systemContext,
}
```

### Gemini

Gemini has no native system-instruction channel in the current runner. Phase 1
shipped an interim fallback:

```typescript
const userPrompt = systemContext
  ? `${systemContext}\n\n${promptText}`
  : promptText;
```

The subprocess is invoked with `-p userPrompt`. This intentionally merges identity
and task into one CLI argument until a native system channel exists.

No Gemini trust or permission flags are changed in Phase 1; those remain outside
the system prompt wiring scope.

## Binding Scenarios

| Run type | How agent identity is resolved |
| --- | --- |
| Agent session chat | Session tags `source=agent` + `agent_id` |
| Loader script `agent()` call | `AgentDefinitionID` from loader-bound agent |
| Managed project run (v2) | `run.ManagedAgentID` |
| Bare provider string, no agent | No agent identity; MPI-only if catalog exists |

## Testing

### Go (`pkg/agentcompose/agent_system_prompt_test.go`)

- Empty `system_prompt` resolves to `""`
- Session-tagged agent resolves trimmed prompt text
- `writeAgentSystemPromptFile` maps host → guest path and writes content
- Empty prompt skips file creation
- `buildAgentExecSpec` includes or omits `--system-prompt-file`

### Runtime JS (`runtime/javascript/test/system-context.test.ts`)

- Section ordering (Agent Identity before Capabilities)
- Agent-only, MPI-only, both, neither
- MPI-only path matches pre-change injection (no `## Agent Identity`)
- `readSystemPromptFile` trim and missing-file behavior

Runner tests (`runners.test.ts`, `runner-execution.test.ts`) were updated to use
`systemContext` instead of `mpiContext`.

## Security and Operations

- `system_prompt` is workspace-owner controlled (same trust boundary as existing
  agent admin APIs). No new injection surface beyond current agent configuration.
- System prompt files live in the session state tree alongside per-turn prompt
  files. They are subject to the same session lifecycle and cleanup.
- Paths are passed through `shellQuote` in the exec spec; no shell interpolation
  of prompt content.
- Phase 1 does not introduce a hard size limit. Follow existing practical limits
  for prompt files.

## Rollout (Phase 1)

| Area | Change |
| --- | --- |
| Database | None |
| API / Proto | None |
| Guest image | Runtime JS changes are merged; production guest image rebuild follows the normal release process. Dev mounts can pick up JS without rebuild. |
| Behavior | Non-empty `system_prompt` takes effect after deploy; empty prompt preserves pre-change MPI-only behavior |

## File Change Map (Phase 1)

| File | Change |
| --- | --- |
| `pkg/agentcompose/service.go` | `resolveAgentSystemPrompt`, `writeAgentSystemPromptFile`, `executeAgentRun`, `buildAgentExecSpec`, compatibility retry |
| `pkg/agentcompose/exec.go` | `AgentDefinitionID` on `ExecuteAgentRequest`; inject `configDB` into `Executor` |
| `pkg/agentcompose/loader_manager.go` | Pass agent definition id into agent execution |
| `pkg/agentcompose/run_service.go` | Pass `ManagedAgentID` into agent execution |
| `pkg/agentcompose/agent_system_prompt_test.go` | **new** — host resolution, file write, exec spec tests |
| `runtime/javascript/src/system-context.ts` | **new** — composition and file read helpers |
| `runtime/javascript/src/cli.ts` | `--system-prompt-file` option |
| `runtime/javascript/src/prompt.ts` | Compose `systemContext` before runner dispatch |
| `runtime/javascript/src/types.ts` | `systemContext` on `RunnerOptions` |
| `runtime/javascript/src/runners/codex.ts` | `developer_instructions` from `systemContext` |
| `runtime/javascript/src/runners/claude.ts` | `systemPrompt.append` from `systemContext` |
| `runtime/javascript/src/runners/gemini.ts` | Prepend `systemContext` to `-p` |
| `runtime/javascript/test/system-context.test.ts` | **new** — composition unit tests |
| `runtime/javascript/test/runners.test.ts` | Updated for `systemContext` |
| `runtime/javascript/test/runner-execution.test.ts` | Updated for `systemContext` |
| `docs/design/agent-compose-runtime-js_contract.md` | Document new flag and layering |
| `openspec/specs/agent-system-prompt/spec.md` | Canonical requirements |

## Success Criteria (Phase 1, verified)

1. Agent with `system_prompt: "Reply only in Chinese"` obeys after Codex/Claude chat run.
2. Empty `system_prompt` → identical to pre-change MPI-only behavior.
3. Codex session resume after prompt edit uses new instructions.
4. Loader bound to an agent definition inherits `system_prompt`.
5. `task test`, runtime JS tests, and `task lint` pass on touched packages.

## Next Steps

The items below were **not** implemented in Phase 1. They are planned follow-ups.

### Platform runtime brief

Add a workspace- or deployment-level brief layer above Agent Identity and MPI.
Would cover platform guardrails, issue workflow semantics, and comment formatting
rules not tied to a single agent definition.

### Workspace global context

Introduce a workspace-level context field distinct from per-agent
`system_prompt`.

### File-based workspace injection

Inject marker blocks into `AGENTS.md`, `CLAUDE.md`, or similar discovery files
with safe cleanup on run completion. Aligns with native Codex/Claude workspace
discovery for `local_directory` workspaces.

### Skills in system context

Discover and inject skill summaries or on-demand `SKILL.md` sections into the
composed brief, similar to Cursor Agent Skills.

### Gemini native system channel

Replace the `-p` prepend fallback when the Gemini CLI or SDK exposes a dedicated
system-instruction parameter. Until then, per-turn message isolation remains
relaxed for Gemini only.

### Field rename: `system_prompt` → `instructions`

Rename for clearer semantics in API and UI. Requires Proto, DB migration, UI,
and client updates.

### Force fresh Codex thread on prompt change

If a future SDK version stops applying constructor `config` on resume, the host
could detect a hash of `system_prompt` and force a new thread when instructions
change. Phase 1 relies on current SDK behavior.

### Prompt file soft size limit

Document a soft recommendation (e.g. < 8 KiB) for `system_prompt` and combined
system context size.

### Frontend surfacing

The Agents UI already edits `system_prompt`. `docs/README.md` notes that the
field is runtime-active. Optional follow-up: in-app hints or expanded user docs.
