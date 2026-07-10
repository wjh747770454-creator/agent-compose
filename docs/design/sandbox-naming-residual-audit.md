# Sandbox Naming Residual Audit

This document is the reusable residual `session` naming classification for the
sandbox naming convergence work. It is a stage-1 characterization artifact: it
records which residual names are intentionally compatible today, before later
stages rename internal and v2 surfaces to sandbox/thread.

Audit command:

```bash
rg -n "\bsession\b|session_id|sessionId|Session" cmd pkg proto runtime docs README.md .env.example Dockerfile docker-compose.yml docker-compose.override.yml
```

Allowed residual categories:

| Category | Boundary | Examples |
| --- | --- | --- |
| v1 compatibility | v1 proto, v1 generated code, v1 Connect service registration, v1 handler/model mapping, and tests that lock v1 wire shape | `SessionService`, `session_id`, `agent_session_id`, `SessionSummary` |
| Deprecated aliases | User-facing compatibility aliases that intentionally forward to sandbox-native behavior while warning | `agent-compose inspect session`, `scheduler.session.*`, `sessionPolicy`, `sessionEnv`, `session_env` |
| Auth/browser session | UI login, OAuth, cookies, or other browser authentication state unrelated to runtime sandboxes | `AUTH_SESSION_TTL`, browser login session copy |
| Provider-native protocol | Third-party agent provider resume identifiers and CLI flags that use native session terminology at adapter boundaries | Claude `session_id`, OpenCode `--session`, provider JSON `sessionId` |
| Migration/error copy | Breaking-change, old-data rejection, or diagnostic text that names old variables, old paths, or old schema | `SESSION_ROOT`, `<DATA_ROOT>/sessions`, old SQLite `session_id` columns |

Current stage-1 characterization anchors:

- v1 compatibility is locked by API mapping tests that assert v1 responses still
  expose `session_id` and `agent_session_id`.
- v2 sandbox-native entry points are locked by API tests that assert
  `RemoveSandbox` receives and returns `sandbox_id`, while today's
  implementation may still delegate through session storage internally.
- CLI deprecated alias behavior is locked by tests for `inspect session`, which
  must warn and produce the same inspect payload as `inspect sandbox`.
- Loader deprecated aliases are locked by tests for `sessionPolicy`,
  `session_policy`, `sessionEnv`, and `session_env`.
- Runtime provider state and SDK parser compatibility are locked by existing
  JavaScript tests that read/write or parse `sessionId`.

Final acceptance is not zero residuals. Every residual returned by the audit
command must be classified into one of the categories above, and residuals in
internal domain, v2 API, runtime env, SQLite schema, deployment defaults, or new
current-state documentation must be removed unless they are explicitly part of a
compatibility or migration boundary.
