## 1. Cross-repo contract (design freeze)

- [x] 1.1 Confirm GitHub path `github.com/tokenlive/tokenlive-standalone` and binary name `tokenlive`
- [x] 1.2 Document embed API sketch (gateway + admin function signatures) in this change or ADR → `embed-api.md`
- [x] 1.3 Document version pin policy (standalone go.mod pins gateway@tag + admin@tag) → `embed-api.md`

## 2. Gateway library surface (`tokenlive-gateway`)

- [x] 2.1 Extract/export embed factory: build Engine + deps from config without `cmd/server` only path → `internal/bootstrap` + `pkg/gateway.New`
- [x] 2.2 Export `RegisterLLMRoutes` (or equivalent) onto caller-owned Gin engine → `Gateway.RegisterGin`
- [x] 2.3 Ensure `GatewayProvider` can be injected from outside (for standalone Embedded provider) → `Options.Provider`
- [x] 2.4 Export config hot-update path (`UpdateConfig` / cache clear hooks) usable by host → `UpdateEngineConfig` / `PurgeAPIKeyCache` / `UpdateYAMLConfig`
- [x] 2.5 Keep `cmd/server` standalone behavior; add smoke test that embed API starts under test main → `pkg/gateway/gateway_test.go`
- [ ] 2.6 Tag a release for standalone to depend on

## 3. Admin library surface (`tokenlive-admin`)

- [x] 3.1 Export `RegisterAdmin` / module init onto caller-owned Gin engine → `adminapp.New/Register`
- [x] 3.2 Export DB open + AutoMigrate + optional default admin seed → via `bootstrap.Init` + mods Init
- [x] 3.3 Export SPA static FS or documented dist path for host mount → `Options.StaticDir` + existing static middleware
- [x] 3.4 Add host callback/hook on config-affecting writes (no import of gateway) → `util.OnConfigChanged` / `NotifyConfigChanged`
- [x] 3.5 Keep existing admin `main` dual-process entry unchanged → `bootstrap.Run` 仍走 CLI
- [ ] 3.6 Tag a release for standalone to depend on

## 4. Create `tokenlive-standalone` repo

- [x] 4.1 Create repo scaffold: `cmd/tokenlive`, `internal/assemble`, `internal/confighub`, `internal/bridge`, `config/`
- [x] 4.2 `go.mod` require gateway + admin; dev `replace` for local paths (tags when released)
- [x] 4.3 Implement assemble: one Gin, register LLM + Admin + health → `internal/assemble`
- [x] 4.4 Implement ConfigHub + EmbeddedGatewayProvider + bridge from admin hooks → `internal/confighub` + `bridge` + `adminapp.LoadGatewaySnapshot`
- [x] 4.5 Startup validation: all-in-one only; require `embedded`; fail-fast otherwise
- [x] 4.6 All-in-one YAML example → `config/all-in-one.example.yml`
- [x] 4.7 Atomic graceful shutdown → `App.ListenAndServe` + `Close`
- [x] 4.8 README: two deployment modes; point mainline to gateway/admin repos

## 5. Homebrew & release

- [x] 5.1 Makefile/release build for `tokenlive` binary (+ frontend dist strategy) → `scripts/package-release.sh`
- [x] 5.2 Local brew-style install + LaunchAgent → `scripts/brew-install-local.sh` + Formula draft
- [x] 5.3 Default brew config + data dir conventions → `config/brew.yml`, prefix etc/var/share
- [ ] 5.4 Formal tap (`tokenlive/homebrew-tokenlive`) after module tags (no path replace)
- [ ] 5.5 Smoke: install/start → admin login → create model → `POST /v1/chat/completions`

## 6. Verification

- [ ] 6.1 CI gateway: standalone cmd + embed API unit/smoke
- [ ] 6.2 CI admin: dual-process cmd + embed API unit/smoke
- [ ] 6.3 CI standalone: all-in-one integration (embedded hot-reload, API key auth)
- [x] 6.4 CI standalone: fail-fast non-embedded config → `cmd/tokenlive` test + ValidateAllInOne
- [x] 6.5 Local smoke: `make smoke` / health on :2525 with bundled configs/admin + forced SQLite
- [ ] 6.6 Archive note: implementation lives in `tokenlive-standalone`; this openspec change is the cross-repo contract
