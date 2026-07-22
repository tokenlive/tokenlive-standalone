# TokenLive Standalone

English | [中文版](./README-zh.md)

> All-in-one LLM API gateway + admin console in a single binary.

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

## Deployment Modes

| Mode | Repos / Artifacts | Best for |
|------|-------------------|----------|
| **Separate (primary)** | [tokenlive-gateway](https://github.com/tokenlive/tokenlive-gateway) + [tokenlive-admin](https://github.com/tokenlive/tokenlive-admin) | Production, multi-instance |
| **All-in-one (this repo)** | `tokenlive` binary | Single-host / Homebrew |

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

## Status

- [x] Scaffold + OpenSpec contract
- [x] Gateway / Admin embed API
- [x] ConfigHub + hot-reload bridge
- [x] Published tags + official Homebrew tap
- [x] Tag-triggered brew release Action
- [ ] Full E2E (login → configure model → chat completions)

## License

Apache 2.0
