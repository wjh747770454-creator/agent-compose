# agent-compose deployment bundle

Installer to set up and run agent-compose with Docker Compose, on Linux
**x86_64 (amd64)** and **arm64**. The container images are multi-arch and are
pulled from the registry at install time (Docker selects the right architecture
automatically), so the installer itself is tiny.

## Release assets

| Asset | Purpose |
|-------|---------|
| `agent-compose-installer.run` | Single-file self-extracting installer (arch-agnostic) |
| `agent-compose-installer.tar.gz` | Same installer as a tar archive |
| `install.sh` | Standalone installer for `curl \| bash` |
| `agent-compose-<version>-<os>-<arch>.tar.gz` | Go binary archive |
| `agent-compose-<version>-<os>-<arch>.tar.gz.sha256` | Per-binary checksum |
| `SHASUMS256.txt` | Checksums |

## Quick start

### Single-file installer (recommended)

```bash
chmod +x agent-compose-installer.run
./agent-compose-installer.run                      # install with defaults
./agent-compose-installer.run -- --dir /opt/agent-compose --port 8080
```

Arguments after `--` are passed to the embedded installer (see Options below).

### One-line install

```bash
curl -fsSL https://github.com/chaitin/agent-compose/releases/latest/download/install.sh | bash
```

### From the installer archive

```bash
tar -xzf agent-compose-installer.tar.gz
cd agent-compose-installer
./install.sh
```

On first run the installer generates an admin password and prints it once:

```
================ agent-compose is ready ================
  URL:        http://localhost:80
  Login credentials (generated, shown only once):
    Username: admin
    Password: <random>
========================================================
```

## Options

```
./install.sh --dir /opt/agent-compose --port 8080
./install.sh --version v1.2.3         # specific release (remote mode)
./install.sh --image-prefix registry.example.com/agent-compose   # mirror / private registry
./install.sh --upgrade                # update an existing install to this release
./install.sh --no-start               # write files but don't pull images or start
```

## Requirements

- Docker Engine + Docker Compose v2
- Network access to the image registry (ghcr.io by default; use
  `--image-prefix` to pull from a mirror or private registry)
- `/dev/kvm` only if you enable the BoxLite or Microsandbox runtime drivers
  (the default `docker` driver does not need it)

## Manage

```bash
cd <install-dir>
docker compose ps
docker compose logs -f
docker compose down
```

Configuration lives in `<install-dir>/.env`; edit and re-run
`docker compose up -d` to apply changes. See `SECURITY.md` in the repository
before exposing the daemon beyond a trusted network.

Re-running the installer refreshes the compose/nginx files and fills missing
secrets or image refs, but it does not overwrite image refs already set in
`.env` unless `--upgrade` is passed. Use `--upgrade` with a newer
`agent-compose-installer.run` to update an existing installation to that
release and restart the stack.
