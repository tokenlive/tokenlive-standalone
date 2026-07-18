## ADDED Requirements

### Requirement: Gateway exposes embeddable library API
The `tokenlive-gateway` module SHALL expose APIs that allow an external process (specifically `tokenlive-standalone`) to construct the LLM Engine and register LLM routes on a provided HTTP router without starting Gateway’s own `cmd/server` main.

#### Scenario: External main hosts Engine
- **WHEN** `tokenlive-standalone` imports the gateway module and calls the documented embed entrypoints
- **THEN** it can serve `/v1/*` LLM APIs using the Gateway Engine pipeline in-process

#### Scenario: Standalone gateway cmd unchanged
- **WHEN** operators run `tokenlive-gateway`’s existing server entrypoint
- **THEN** behavior remains valid for standalone deployment (no requirement to use tokenlive-standalone)

### Requirement: Admin exposes embeddable library API
The `tokenlive-admin` module SHALL expose APIs that allow an external process to register management routes, auth, and console static assets on a provided `*gin.Engine` (or agreed router) and to initialize the management database.

#### Scenario: External main hosts Admin
- **WHEN** `tokenlive-standalone` imports the admin module and calls the documented embed entrypoints
- **THEN** it can serve `/api/v1/*` and the admin SPA in-process

#### Scenario: Standalone admin cmd unchanged
- **WHEN** operators run `tokenlive-admin`’s existing server entrypoint
- **THEN** dual-process Admin behavior remains valid

### Requirement: Admin change notifications without importing gateway
Admin embed APIs SHALL support notifying the host process of configuration-affecting mutations (models, endpoints, policies, API keys) via a host-supplied callback or interface, without the admin module importing `tokenlive-gateway`.

#### Scenario: Host receives change callback
- **WHEN** an admin write succeeds that affects gateway runtime config
- **THEN** the host-supplied callback is invoked so `tokenlive-standalone` can refresh ConfigHub/Engine

### Requirement: No reverse module dependency
Neither `tokenlive-gateway` nor `tokenlive-admin` SHALL add a Go module dependency on `tokenlive-standalone`.

#### Scenario: Mainline modules stay independent
- **WHEN** building gateway or admin mainline binaries
- **THEN** the build does not require the standalone module
