# Native Agent Delegation for Project Runs

Status: accepted for implementation (phase 2 minimum slice)

## Context

Project v2 already persists `Project`, `Run`, and `Sandbox`, but an agent running
inside a guest cannot delegate work to another named agent in the same Project.
`runtime.agent()` starts another provider runner in the current guest; it does
not resolve an `AgentDefinition`, create a child Run, or produce delegation
audit history. Structured output is also returned only to the guest caller and
is not stored as a first-class Run field.

The first required path is `entry -> project-intelligence`. The design must
reuse the existing Project Run controller and must not introduce a parallel
scheduler or trust a guest to choose a Project or parent Run.

## Decisions

### Run lineage

Every delegated Run stores:

- `parent_run_id`: immediate caller Run.
- `root_run_id`: top-level Run; a top-level Run uses its own Run ID.
- `delegation_id`: stable logical delegation identifier across retries.
- `delegation_attempt`: one-based execution attempt.
- `delegation_reason`: short caller-supplied audit reason.

`target_agent` is represented by the existing `agent_name`. Delegation state is
derived from child Run status; it is not duplicated in another mutable table.
The API can filter Runs by `parent_run_id` or `root_run_id`.

### Trusted delegation boundary

The guest calls a dedicated runtime endpoint using `CAP_TOKEN`. The server:

1. resolves the token to a running Sandbox;
2. resolves that Sandbox to exactly one active parent Run;
3. derives the Project and root Run from that parent;
4. verifies the target is an enabled Agent in the same Project;
5. creates and executes the child through the existing v2 Run controller.

The guest never supplies `project_id`, `parent_run_id`, `root_run_id`, driver,
volumes, environment overrides, or an arbitrary Sandbox ID. The generic
`RunService.RunAgent` remains an operator-facing API and is not exposed as the
guest delegation boundary.

### Runtime SDK contract

The JavaScript SDK exposes:

```ts
runtime.delegate("project-intelligence", prompt, {
  outputSchema,
  timeoutMs,
  idempotencyKey,
  reason,
});
```

It returns the child Run ID, lineage, attempt, status, validated structured
result, text output, warnings, and a classified error. The endpoint response is
the source of truth; the parent does not scrape a transcript.

### Structured result

`structured_result_json` is a first-class nullable Run field distinct from
`result_json`. `result_json` remains execution metadata for compatibility.
When an output schema is requested, successful execution must promote the
provider's final structured payload into `structured_result_json` after JSON
and schema validation. A Run cannot be reported as a successful structured
delegation when that field is absent or invalid.

### Failure and retry semantics

The delegation service may retry once for transient infrastructure failures.
Validation failures, invalid requests, missing/disabled target Agents, and
deterministic provider failures are returned without blind retry. All attempts
share `delegation_id` and use incrementing `delegation_attempt` values.

The service returns all attempt Run IDs, error classification, and partial
output. Business takeover belongs to the parent Agent: `entry` can analyze the
input itself or ask the user. The framework records takeover when the parent
reports it, but does not invent a business conclusion.

### Audit events

Run status events remain authoritative for execution. Delegation additionally
records lifecycle events with these names:

- `delegation.created`
- `delegation.started`
- `delegation.succeeded`
- `delegation.failed`
- `delegation.retried`
- `delegation.taken_over`

Event payloads include delegation ID, parent/root/child Run IDs, target Agent,
attempt, and error classification where applicable. They must not contain the
sandbox credential.

### Cancellation and cleanup

Request cancellation is propagated to a child execution. A caller timeout does
not rewrite an already terminal child. Child cleanup uses the existing Run
cleanup policy. Phase 2 defaults delegated children to remove-on-completion and
does not reuse the parent Sandbox.

## Delivery slices

1. Persist and expose Run lineage, including list filters and compatibility
   migration for existing SQLite databases.
2. Add the authenticated runtime delegation endpoint and SDK client.
3. Promote and validate structured results.
4. Add retry classification and delegation audit events.
5. Verify `entry -> project-intelligence` end to end without CRM or DingTalk
   writes.

## Non-goals

- A second orchestration database or scheduler.
- Cross-Project delegation.
- Guest-selected credentials, Project IDs, drivers, mounts, or environment.
- Framework-generated business conclusions.
- Real CRM or DingTalk mutations during the phase 2 regression.
