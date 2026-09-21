# TokenLive Standalone

English | [中文版](./README-zh.md)

> All-in-one LLM API gateway + admin console in a single binary.

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

## Deployment Modes

| Mode | Repos / Artifacts | Best for |
|------|-------------------|----------|
| **Separate (primary)** | [tokenlive-gateway](https://github.com/tokenlive/tokenlive-gateway) + [tokenlive-admin](https://github.com/tokenlive/tokenlive-admin) | Production, multi-instance |
| **All-in-one (this repo)** | `tokenlive` binary | Single-host / Homebrew / Linux / Docker |

This repo does **not** replace the primary dual-process deployment. Both Gateway and Admin must run together.

## Install

### Homebrew (macOS)

```bash
brew tap tokenlive/tokenlive
brew install tokenlive
brew services start tokenlive
# http://127.0.0.1:2525  —  admin / admin
```

Stop: `brew services stop tokenlive`

Uninstall:

```bash
brew uninstall tokenlive
brew untap tokenlive/tokenlive
```

Details: [docs/homebrew.md](docs/homebrew.md)

### Linux (tarball + systemd)

One-line install (auto-detects arch, installs binary + config + systemd service):

```bash
curl -fsSL https://github.com/tokenlive/tokenlive-standalone/releases/download/v1.0.0/tokenlive-1.0.0-linux-services.tar.gz | tar -xz
sudo bin/install-linux.sh 1.0.0
# http://127.0.0.1:2525  —  admin / admin
```

Manage the service:

```bash
sudo systemctl status tokenlive
sudo systemctl restart tokenlive
journalctl -u tokenlive -f        # logs
```

Paths: binary `/usr/local/bin/tokenlive`, config `/etc/tokenlive/config.yml`, data `/var/lib/tokenlive`.

### Docker

```bash
docker run -d --name tokenlive \
  -p 2525:2525 \
  -v tokenlive-data:/var/lib/tokenlive \
  -v tokenlive-config:/etc/tokenlive \
  --restart unless-stopped \
  ghcr.io/tokenlive/tokenlive:latest
# http://127.0.0.1:2525  —  admin / admin
```

Override config by mounting a custom `config.yml` at `/etc/tokenlive/config.yml`.

### From Source

Prerequisites: sibling checkouts at the same level.

```
Projects/
  tokenlive-gateway/
  tokenlive-admin/
  tokenlive-standalone/   # this repo
```

```bash
go mod tidy
make run          # http://127.0.0.1:2525
make smoke        # start briefly and check health
```

To build the admin SPA:

```bash
cd ../tokenlive-admin/frontend && npm ci && npm run build:prod
cd ../../tokenlive-standalone && make run
```

### Pre-built Release

Download from [GitHub Releases](https://github.com/tokenlive/tokenlive-standalone/releases).

## Architecture

```
tokenlive (this repo)
  ├─ adminapp (tokenlive-admin)       → /api/v1 + SPA
  ├─ confighub                        → Embedded GatewayProvider
  └─ pkg/gateway (tokenlive-gateway)  → /v1/* Engine
```

Admin writes DB → `OnConfigChanged` → ConfigHub refresh → `ApplyGatewayConfig` / cache purge.

## Configuration

| Flag | Description |
|------|-------------|
| `-conf` | Gateway YAML (must set `gateway.config_source: embedded`) |
| `-data-dir` | Data directory (default: `data`) |
| `-admin-workdir` | Admin TOML directory (default: bundled `configs/admin`) |
| `-admin-config` | Subset of admin config; omit for bundled defaults |
| `-admin-static` | SPA directory, optional |

Example YAML: `config/all-in-one.example.yml`
Bundled admin config: `configs/admin/`

Default port: **2525**. Default database: SQLite (`data/tokenlive.db`). Default admin: `admin` / `admin` (captcha disabled).

### Product identity and update notices

All-in-one reports one **standalone** product version, injected by the running
executable into Admin. Its embedded Gateway is not reported as a separate
professional node. A frontend version or image tag is not executable identity;
development builds remain non-comparable even when their name resembles a
stable release.

Homebrew is recognized only by the installed
`libexec/tokenlive-install-channel` marker relative to the resolved executable.
Source, Linux, Docker, or unmarked copies remain `install_channel=unknown`;
they show the current version without assuming a Homebrew or professional
upgrade source. Do not add an installation marker just to enable a command
intended for a different package manager.

For confirmed Homebrew installs, runtime readiness is inferred from the stable
version in the official tap's currently published Formula. The runtime does not
make additional requests to verify Release metadata or asset availability, and
there is no historical-release fallback. The publisher establishes the ordering:
upload the required assets and confirm the Release is public before updating the
tap. Publishing assets alone is not Homebrew-ready; the tap push must also
succeed before the publisher emits `Homebrew ready` / `homebrew_ready=true`.

Admin checks asynchronously at startup, then every six hours; a check has a
shared five-second deadline. `UPDATE_CHECK_ENABLED=false` disables both automatic
and manual external checks without hiding the local version.
`UPDATE_CHECK_INTERVAL_SECONDS` overrides the interval. The shared per-Admin
manual cooldown is 60 seconds; opening About reads cached results only.
Root has update-management permission by default. Other roles can be granted
`system.versionUpdates`; ordinary logged-in users can still view current
versions, but not update-only data or trigger checks. The backend enforces this.

The UI only offers release information and copyable instructions. For Homebrew,
the operator may run `brew update` and `brew upgrade tokenlive` after reviewing
the release and backups; installation and any service restart remain manual.
The first upgrade to a version containing this feature must also be manual.

## Development

```bash
make test
make run
make smoke
```

Release packaging:

```bash
VERSION=0.2.0 BREW_PREFIX="$(brew --prefix)" ./scripts/package-release.sh
```

Local Homebrew install (from source):

```bash
./scripts/brew-install-local.sh
```

Push a `vX.Y.Z` tag to run the full brew release chain (tarball + GitHub Release + tap Formula update). See [docs/homebrew.md](docs/homebrew.md).

### Cross-repository validation and release prerequisites

The version integration test in `internal/assemble/version_integration_test.go`
uses the real Gateway HTTP and Redis Senders with the public `adminapp` facade.
It verifies grouping, dual-channel deduplication, invalid tokens, three-minute
expiry, removal of obsolete update notices without another source fetch, and
internal reporting while external checks are disabled. It uses per-case child
processes, temporary SQLite/config directories, miniredis, and a controlled
Release transport; it does not use real users, business Redis, or the internet.
Each test record has an explicit deletion cleanup and TTL; temporary files,
servers, clients and processes are cleaned up by the test.

Use a temporary `go.work` for jointly testing reviewed local Admin/Gateway
checkouts; do not commit workstation `replace` directives or workspaces.
The declared dependencies (`tokenlive-admin v0.9.10` and `tokenlive-gateway v0.9.10`)
provide the official `pkg/productversion` and `pkg/versionreport` packages for
version identity and upgrade notifications.

The current-source `package-release.sh` graph was separately built with real Go
without the temporary genproto override used by the joint test workspace.
Actual Docker image compilation/container lifecycle and remote release jobs
still require separate verification; no image publication or service actions
were part of the local validation. The frontend tests were verified on Node
22.23.1, not the pre-existing Node 18 build workflow.

## Status

- [x] Scaffold + OpenSpec contract
- [x] Gateway / Admin embed API
- [x] ConfigHub + hot-reload bridge
- [x] Published tags + official Homebrew tap
- [x] Tag-triggered brew release Action
- [x] Linux tarball (amd64 + arm64) + systemd
- [x] Docker image (multi-arch) on ghcr.io
- [ ] Full E2E (login → configure model → chat completions)

## License

Apache 2.0
