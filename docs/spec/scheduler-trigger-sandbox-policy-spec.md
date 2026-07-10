# Scheduler Trigger Sandbox Policy Specification

## Goal

Allow an agent scheduler to define the default sandbox lifecycle policy for its
agent calls and allow each declarative trigger to override that default.

```yaml
agents:
  reviewer:
    scheduler:
      sandbox_policy: sticky
      triggers:
        - name: incremental-review
          cron: "0 * * * *"
          prompt: Review the current workspace state.
        - name: clean-review
          cron: "0 0 * * *"
          prompt: Review the workspace in a fresh sandbox.
          sandbox_policy: new
```

## Configuration Contract

Both `agents.<name>.scheduler.sandbox_policy` and
`agents.<name>.scheduler.triggers[].sandbox_policy` accept exactly:

- `new`: create a sandbox for the run and use the existing completion cleanup
  behavior.
- `sticky`: keep and reuse a sandbox for subsequent calls in the same binding
  scope.

The effective policy is resolved in this order:

1. trigger `sandbox_policy`, when present;
2. scheduler `sandbox_policy`, when present;
3. `new`.

Keeping `new` as the default preserves the behavior of existing compose files.
Unsupported or blank explicit values fail compose normalization with the full
field path.

The normalized project spec, canonical hash input, CLI config output, and v2
project spec response expose both fields. The v1 API is unchanged.

## Sticky Binding Scope

Sticky sandboxes are isolated by loader and runtime trigger:

```text
(loader_id, trigger_id) -> sandbox_id
```

Consequences:

- repeated runs of one trigger reuse its sandbox;
- different triggers under the same scheduler do not share a sandbox;
- multiple `scheduler.agent` calls in one trigger callback share that trigger's
  sandbox;
- calls outside a trigger callback, such as loader `main()`, use an empty
  trigger ID and therefore the scheduler-level binding.

The trigger ID comes from loader execution state and is not a caller-controlled
`scheduler.agent` option.

Existing loader bindings migrate to an empty trigger ID so non-project loaders
retain their current sticky sandbox.

## Execution Behavior

Declarative triggers compile their explicit override into the existing
`scheduler.agent` `sandboxPolicy` option. When no override exists, the generated
call omits the option and inherits the managed loader's scheduler policy.

Inline scheduler scripts retain their existing call-level override:

```js
scheduler.agent("Review the workspace", { sandboxPolicy: "sticky" });
```

Scheduled and manually invoked project triggers use the same effective policy
and binding scope. For a sticky run, the project-run path loads or resumes the
bound sandbox and keeps it running on completion. A missing, deleted, or
unrecoverable binding falls back to a newly created sandbox and replaces the
binding. A `new` run ignores any old sticky binding.

Changing a trigger from `sticky` to `new` does not implicitly stop or delete an
already running sandbox during project apply; normal sandbox lifecycle commands
remain responsible for cleanup.

## Validation and Compatibility

Coverage must prove parsing, normalization, canonical hashing, protobuf/YAML
mapping, generated scheduler calls, binding migration and persistence, automatic
and manual trigger reuse, trigger isolation, policy overrides, stale-binding
fallback, and inline-script behavior.

The feature is complete when `task lint`, `task build`, and `task test` pass.
This specification is an implementation aid and must be removed in the final
commit after the durable design documentation has been updated.
