## ADDED Requirements

### Requirement: Standalone mainline config sources unchanged
In standalone deployment, Gateway SHALL continue to support `config_source` values `local`, `redis`, and `http` without requiring `tokenlive-standalone` or in-process Admin.

#### Scenario: HTTP mode
- **WHEN** standalone Gateway uses `config_source=http` and `admin_url`
- **THEN** it obtains config from external Admin HTTP APIs as today

#### Scenario: Redis mode
- **WHEN** standalone Gateway uses `config_source=redis`
- **THEN** it reads shared Redis keys as today

#### Scenario: Local mode
- **WHEN** standalone Gateway uses `config_source=local`
- **THEN** it uses local YAML without a management DB

### Requirement: Embedded config is owned by tokenlive-standalone
In all-in-one mode, `tokenlive-standalone` SHALL supply Engine configuration via an in-process embedded provider (`config_source=embedded`) backed by Admin domain data in the same process, without external admin HTTP polling.

#### Scenario: Start with embedded
- **WHEN** `tokenlive-standalone` starts with embedded config
- **THEN** the Engine receives models/endpoints/providers/policies/apikeys without `admin_url` or `sync_token`

#### Scenario: Reject non-embedded all-in-one
- **WHEN** all-in-one is started with `config_source` other than `embedded`
- **THEN** startup MUST fail with a clear error

#### Scenario: No external poller
- **WHEN** all-in-one runs with embedded
- **THEN** HTTP polling of external `/api/v1/gateway/config|policies|apikeys` is not used for primary config

### Requirement: In-process hot apply
Admin mutations in all-in-one SHALL update the live Engine through the standalone bridge/ConfigHub without process restart.

#### Scenario: Endpoint change
- **WHEN** an operator updates a model endpoint via in-process Admin API
- **THEN** subsequent LLM requests use the new endpoint selection without restart

#### Scenario: Policy change
- **WHEN** an operator updates a governance policy via in-process Admin API
- **THEN** subsequent requests observe the new policy without external Redis pub/sub from another admin process

### Requirement: API keys from embedded store in all-in-one
All-in-one SHALL validate client API keys against the in-process admin store via the embedded provider.

#### Scenario: Valid key
- **WHEN** the key exists and is active in the embedded store
- **THEN** authentication succeeds

#### Scenario: Unknown key
- **WHEN** the key is absent
- **THEN** authentication fails without calling an external Admin HTTP API
