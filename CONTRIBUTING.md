# Contributing

Thanks for considering a contribution to agent-compose.

The project is still in preview. Please keep changes focused, explain behavior
changes clearly, and include tests for user-visible behavior.

## Development Setup

Install Go, Node.js, npm, Docker when needed, and Task.

```bash
npm ci
cd runtime/agent-compose-runtime-sdk && npm ci
cd ../../runtime/javascript && npm ci
```

Build and test from the repository root:

```bash
task lint
task build
task test
```

For smaller loops:

```bash
go test ./cmd/... ./pkg/...
cd runtime/agent-compose-runtime-sdk && npm test
cd runtime/javascript && npm run test:unit
```

Some runtime smoke tests require Docker, BoxLite, Microsandbox, KVM, or platform
specific runtime artifacts.

## Pull Requests

- Keep PRs scoped to one change.
- Include a clear problem statement and solution summary.
- Update documentation when behavior, configuration, or user workflows change.
- Add or update tests for bug fixes and new functionality.
- Avoid committing generated runtime state, local data, credentials, or private
  infrastructure configuration.

## Code Style

- Follow existing Go package patterns.
- Prefer small, local changes over broad refactors.
- Keep API handlers thin where possible and put reusable behavior in domain
  helpers.
- Use structured configuration and existing helper APIs instead of ad hoc
  parsing.

## Security

Do not include secrets, private registry endpoints, internal certificates,
tokens, or personal local state in commits.

Report suspected vulnerabilities through the process in [SECURITY.md](SECURITY.md).
