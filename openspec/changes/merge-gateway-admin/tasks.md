## 1. Cross-repo contract (design freeze)

- [ ] 1.1 Confirm GitHub path `github.com/tokenlive/tokenlive-standalone` and binary name `tokenlive`
- [ ] 1.2 Document embed API sketch (gateway + admin function signatures) in this change or ADR
- [ ] 1.3 Document version pin policy (standalone go.mod pins gateway@tag + admin@tag)

## 2. Gateway library surface (`tokenlive-gateway`)

- [ ] 2.1 Extract/export embed factory: build Engine + deps from config without `cmd/server` only path
- [ ] 2.2 Export `RegisterLLMRoutes` (or equivalent) onto caller-owned Gin engine
- [ ] 2.3 Ensure `GatewayProvider` can be injected from outside (for standalone Embedded provider)
- [ ] 2.4 Export config hot-update path (`UpdateConfig` / cache clear hooks) usable by host
- [ ] 2.5 Keep `cmd/server` standalone behavior; add smoke test that embed API starts under test main
- [ ] 2.6 Tag a release for standalone to depend on

## 3. Admin library surface (`tokenlive-admin`)

- [ ] 3.1 Export `RegisterAdmin` / module init onto caller-owned Gin engine
- [ ] 3.2 Export DB open + AutoMigrate + optional default admin seed
- [ ] 3.3 Export SPA static FS or documented dist path for host mount
- [ ] 3.4 Add host callback/hook on config-affecting writes (no import of gateway)
- [ ] 3.5 Keep existing admin `main` dual-process entry unchanged
- [ ] 3.6 Tag a release for standalone to depend on

## 4. Create `tokenlive-standalone` repo

- [ ] 4.1 Create repo scaffold: `cmd/tokenlive`, `internal/assemble`, `internal/confighub`, `internal/bridge`, `config/`
- [ ] 4.2 `go.mod` require gateway + admin tags; dev `replace` for local paths
- [ ] 4.3 Implement assemble: one Gin, register LLM + Admin + health/metrics
- [ ] 4.4 Implement ConfigHub + EmbeddedGatewayProvider + bridge from admin hooks
- [ ] 4.5 Startup validation: all-in-one only; require `embedded`; fail-fast otherwise
- [ ] 4.6 All-in-one YAML example: SQLite, memory state, data_dir, admin JWT/DB
- [ ] 4.7 Atomic graceful shutdown
- [ ] 4.8 README: two deployment modes; point mainline to gateway/admin repos

## 5. Homebrew & release

- [ ] 5.1 Makefile/release build for `tokenlive` binary (+ frontend dist strategy)
- [ ] 5.2 Homebrew Formula/tap for `tokenlive-standalone`
- [ ] 5.3 Default brew config + data dir conventions
- [ ] 5.4 Smoke: install/start → admin login → create model → `POST /v1/chat/completions`

## 6. Verification

- [ ] 6.1 CI gateway: standalone cmd + embed API unit/smoke
- [ ] 6.2 CI admin: dual-process cmd + embed API unit/smoke
- [ ] 6.3 CI standalone: all-in-one integration (embedded hot-reload, API key auth)
- [ ] 6.4 CI standalone: fail-fast non-embedded config
- [ ] 6.5 Archive note: implementation lives in `tokenlive-standalone`; this openspec change is the cross-repo contract
