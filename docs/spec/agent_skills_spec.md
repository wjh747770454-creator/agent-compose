# Agent Skills Spec

**Status:** implemented.

This document specifies how agent-compose will add per-agent `skills` support
to `agent-compose.yml`, host-side materialization, and guest runtime execution.
It intentionally does not cover the v1 AgentDefinition API: `proto/agentcompose/v1`
is expected to be removed and must not be extended for this feature.

Related documents:

- Agent system prompt layering: [../design/agent_system_prompt_design.md](../design/agent_system_prompt_design.md)
- Runtime invocation contract: [../design/agent-compose-runtime_contract.md](../design/agent-compose-runtime_contract.md)
- Runtime mount manifest: [../design/runtime_mount_manifest_design.md](../design/runtime_mount_manifest_design.md)

## Summary

Agent Skills are capability folders centered on a `SKILL.md` file with YAML
frontmatter. agent-compose will let each agent declare a precise skill set in
`agent-compose.yml`. The daemon resolves those declarations from `git`, `file`,
or `zip` sources, validates each `SKILL.md`, caches resolved content, and
projects the active set into the session at:

```text
<session>/home/.agents/skills/<name>/SKILL.md
guest: /root/.agents/skills/<name>/SKILL.md
```

The runtime receives the active names through repeated `--skill <name>`
arguments. Claude, Codex, and OpenCode use native or near-native skills
discovery. Gemini receives a small system-context catalog that points to the
skill folders and may read the full `SKILL.md` itself.

## Goals and Non-Goals

Goals:

- Add `skills:` under each agent in `agent-compose.yml`.
- Support `git`, `file`, and `zip` sources.
- Keep credentials per skill and prevent secrets from entering canonical
  project output, DB diffs, logs, or generated prompt text.
- Persist skills on managed agent definitions so project reconcile and agent
  runs can read the latest definition.
- Expose skills through the v2 project proto surface used by project dry-run and
  change reporting.
- Keep session reuse isolated by reconciling the projected skill directory to
  the current run's exact active skill set.

Non-goals:

- No v1 AgentDefinition API support.
- No frontend/UI editor work.
- No centralized skill registry or marketplace.
- No lockfile or dependency resolver.
- No Gemini-native skills integration until the provider exposes one.
- No automatic cache garbage collection in the first implementation.

## Configuration

`skills` is a structured list with string shorthand for common cases.

```yaml
agents:
  reviewer:
    provider: claude
    skills:
      - name: pdf
        source: git
        url: https://github.com/anthropics/skills.git
        path: skills/pdf
      - name: internal-reviewer
        source: git
        url: https://git.internal/skills/reviewer.git
        ref: v1.2.0
        path: skills/reviewer
        token: ${GIT_TOKEN}
      - ./skills/local-thing
      - name: report
        source: zip
        url: https://example.com/report-skill.zip
        path: report
```

Go compose types:

```go
type SkillSpec struct {
    Name     string `yaml:"name,omitempty" json:"name,omitempty"`
    Source   string `yaml:"source,omitempty" json:"source,omitempty"` // git|file|zip
    URL      string `yaml:"url,omitempty" json:"url,omitempty"`       // git or zip URL
    Path     string `yaml:"path,omitempty" json:"path,omitempty"`     // file root or subdir
    Ref      string `yaml:"ref,omitempty" json:"ref,omitempty"`       // git ref
    Username string `yaml:"username,omitempty" json:"username,omitempty"`
    Password string `yaml:"password,omitempty" json:"password,omitempty"`
    Token    string `yaml:"token,omitempty" json:"token,omitempty"`
}
```

Shorthand rules:

- `./x` or `../x` means `source: file`, with `path` resolved relative to the
  compose file directory.
- `/x` means `source: file`, allowed only when the absolute path is inside a
  configured daemon allowlist.
- A local path or URL ending in `.zip` means `source: zip`.
- A git URL ending in `.git` means `source: git`.
- `github:org/repo//subdir@ref` means `source: git`, `url:
  https://github.com/org/repo.git`, `path: subdir`, and `ref: ref`.

Validation rules:

- `name`, when provided, must be a stable path segment using the existing stable
  identifier rules.
- Names must be unique per agent after inference.
- `git` requires `url`; `file` requires `path`; `zip` requires `url` or `path`.
- `password` and `token` must be variable references such as `${GIT_TOKEN}`.
  Plaintext secrets are rejected. `username` may be plaintext or a variable
  reference.
- Non-secret fields are normalized with existing compose environment
  interpolation. Secret fields are preserved as references and resolved only
  during materialization.

## Data Model and Persistence

The domain layer gets a dedicated skill type instead of reusing compose types.

```go
type AgentSkill struct {
    Name     string `json:"name,omitempty"`
    Source   string `json:"source,omitempty"`
    URL      string `json:"url,omitempty"`
    Path     string `json:"path,omitempty"`
    Ref      string `json:"ref,omitempty"`
    Username string `json:"username,omitempty"`
    Password string `json:"password,omitempty"`
    Token    string `json:"token,omitempty"`
    SourceRoot string `json:"source_root,omitempty"` // internal local-source boundary
}
```

`domain.AgentDefinition` gains `Skills []AgentSkill`. Managed project sync
converts `compose.NormalizedSkillSpec` to `domain.AgentSkill` in `pkg/projects`,
mirroring the existing volume conversion pattern. For compose-managed agents,
`SourceRoot` is set to the compose file directory so local `file` skills and
local zip archives are constrained to the project tree after normalization.

SQLite persistence:

- Add `agent_definition.skills TEXT NOT NULL DEFAULT '[]'`.
- Encode/decode as normalized JSON, similar to env and volumes.
- Existing databases are upgraded through `ensureColumn`; old rows read as an
  empty skill list.

Project/v2 proto:

- `proto/agentcompose/v2.AgentSpec` must include a repeated skill message.
- Project dry-run/change output must preserve skills so compose-driven changes
  are visible outside the daemon.
- `ManagedAgentDefinitionUnchanged` must compare normalized skills so changing
  `agent-compose.yml` updates the managed agent definition.

v1 API:

- Do not add skills to `proto/agentcompose/v1`.
- Do not require v1 create/update/get/list/validate round trips.
- Agents created only through the v1 API do not support skills.

## Materialization and Cache

Create `pkg/skills` for source parsing, validation, caching, and projection
inputs. The package should not depend on `pkg/compose`.

Source behavior:

- `git`: resolve `ref` to a commit SHA using `git ls-remote` before cache
  lookup. Cache key includes normalized URL, resolved commit SHA, and repo
  subdirectory path. If `ref` is omitted, resolve the remote default branch on
  each run and cache by the resulting commit SHA.
- `file`: copy from an allowed local directory. Relative paths are resolved
  against the compose file directory. Absolute paths are allowed only when they
  are inside the compose-managed `SourceRoot` or a configured daemon allowlist.
- `zip`: local zip paths follow the same local path allowlist rules as `file`.
  URL zips are downloaded on each resolution attempt, size-limited, hashed, and
  cached by content hash plus selected subdirectory.

Additional local source roots for non-compose/API-created local skills can be
declared with `AGENT_COMPOSE_SKILL_SOURCE_ROOTS`, using the platform path-list
separator. `DATA_ROOT` and `SESSION_ROOT` are also treated as daemon-owned
local roots.

Cache root:

```text
<DATA_ROOT>/skills
```

Cache writes use the existing image cache pattern: per-cache lock, temporary
directory, validation, atomic promotion, and `.ready` marker. The first
implementation does not perform automatic GC. Operators may manually remove
`DATA_ROOT/skills`; future work may add `skills prune`.

Validation:

- Every resolved skill directory must contain `SKILL.md`.
- `SKILL.md` must have YAML frontmatter with non-empty `name` and
  `description`.
- Frontmatter `name`, configured `name`, and destination directory name must not
  conflict.
- Validation failure fails the run. Runtime-side missing or damaged skills are
  treated as unexpected drift and should warn or fail according to provider
  behavior.

Security:

- Copying and extraction must reject absolute paths and `..` traversal.
- Zip URL downloads must use bounded timeouts, maximum response size, maximum
  expanded size, maximum file count, redirect checks, and SSRF protection
  against loopback, private network ranges, and cloud metadata addresses.
- Logs must redact resolved token/password values.
- Symlinks inside source skill content are not required for the first
  implementation; reject them unless a provider-specific need is proven.

## Session Projection

Add `home/.agents` to the runtime mount manifest:

```text
host:  <session>/home/.agents
guest: /root/.agents
```

`pkg/execution` adds helpers for the host projection:

```text
HostAgentSkillsDir(session) -> <session>/home/.agents/skills
WriteAgentSkills(session, resolvedSkills)
```

`home/.agents/skills` is an agent-compose managed projection directory. Users
must not manually place extra content there; custom skills must be declared
through `git`, `file`, or `zip`.

`WriteAgentSkills` reconciles the directory to the current run's exact active
skill set:

- Write a manifest of managed skill names and source fingerprints.
- Remove only entries previously recorded by this feature's manifest.
- Never delete unrelated files outside `home/.agents/skills`.
- Ensure each active skill is copied into `<name>/`.
- Ensure Claude compatibility through `<session>/home/.claude/skills ->
  ../.agents/skills` when symlinks are supported. If a driver/platform cannot
  expose the symlink, mirror the projected directory as a fallback.

This reconcile step is required because the same session may be reused by
different agents or by the same agent after its skill list changes.

## Execution Flow

`AgentRunner` should resolve the agent definition once per run. The result
contains both `system_prompt` and `skills`; a missing requested agent definition
is an explicit error instead of silently dropping skills.

Run sequence:

1. Write the per-turn prompt file.
2. Write or remove the fixed system prompt file.
3. Resolve `AgentDefinition.Skills` through `pkg/skills`, including secret
   environment references.
4. Project resolved skills with `WriteAgentSkills`.
5. Build the runtime command with one `--skill <name>` per active skill.
6. Execute the provider runtime.

Runtime command contract:

```sh
agent-compose-runtime prompt \
  --provider <provider> \
  --message-file <path> \
  --state-root /data/state \
  --workspace /workspace \
  --home /root \
  --skill <name> \
  --skill <name>
```

The runtime contract docs must remove the obsolete `--system-prompt-file`
parameter and document repeated `--skill`.

## Runtime Provider Behavior

Runtime TypeScript changes:

- `cli.ts` collects repeated `--skill <name>` values.
- `RunnerOptions` gains `skills?: string[]`.
- The skills root is derived from `home`: `/root/.agents/skills` in the guest.

Provider behavior:

| Provider | Behavior |
| --- | --- |
| Claude | Upgrade and verify `@anthropic-ai/claude-agent-sdk` support for `skills` and `settingSources`. Pass the exact skill name list, not `"all"`, and include user settings so `~/.claude/skills` is discovered. |
| Codex | Rely on native discovery of `/root/.agents/skills`. `--skill` is an agent-compose projection input, not a Codex SDK whitelist. Exact exposure is guaranteed by host-side reconcile. |
| OpenCode | Inject a run-scoped configuration that adds `/root/.agents/skills` to `skills.paths`. The injection must not persist into user global `~/.opencode`; tests must prove the chosen CLI/config mechanism. |
| Gemini | Append an `## Agent Skills` section to `systemContext` containing only active `name`, truncated `description`, and absolute skill path. Do not inline full `SKILL.md`. |

The Claude SDK version in the current repository is older than the version
known to expose the required fields. Implementation must upgrade the dependency
and verify generated/runtime types before wiring provider behavior.

## Acceptance Tests

Go tests:

- `pkg/compose`: structured specs, shorthand specs, duplicate names, invalid
  sources, plaintext secret rejection, and relative path resolution from the
  compose file directory.
- `pkg/skills`: git fixture, file fixture, zip fixture, frontmatter validation,
  cache refresh for changed git branch and changed file content, secret
  resolution redaction, and unsafe archive/path rejection.
- `pkg/storage/configstore`: automatic `skills` column addition and read/write
  round trip.
- `pkg/projects`: skills changes make `ManagedAgentDefinitionUnchanged` return
  false and update managed agent definitions.
- `pkg/execution`: projection reconcile removes stale managed skills but does
  not delete unrelated files outside the managed directory.

Project/v2 tests:

- v2 `AgentSpec` skills survive project parse/normalize/dry-run/change output.
- Project sync persists skills to the managed agent definition and the run path
  reads them back from storage.

Runtime tests:

- CLI collects repeated `--skill`.
- Claude query options include exact skills and user setting source after SDK
  upgrade.
- OpenCode uses a run-scoped config with `skills.paths` containing
  `/root/.agents/skills`.
- Gemini appends the compact skill catalog and does not inline complete
  `SKILL.md`.

Integration smoke:

- Docker driver session with local fixture skills materializes
  `home/.agents/skills/<name>/SKILL.md`.
- `home/.claude/skills` points to `../.agents/skills` or an equivalent mirrored
  fallback.
- Reusing a session after adding/removing skills exposes only the current run's
  active set.
- Git and zip integration use local fixtures in CI; public GitHub skills are
  optional manual smoke tests only.

## Implementation Notes

- Prefer shared internal git helpers over exporting workspace-specific package
  internals directly.
- Use structured YAML parsing and existing compose validation patterns instead
  of ad hoc string parsing.
- Keep provider-specific runtime changes small and covered by unit tests before
  adding end-to-end tests.
- Document any final OpenCode config injection mechanism in
  `docs/design/opencode_cli_support.md` after implementation chooses it.
