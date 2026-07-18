## ADDED Requirements

### Requirement: Exactly two deployment modes across the product
The product SHALL support exactly two deployment modes: **standalone** (separate `tokenlive-gateway` and/or `tokenlive-admin` processes) and **all-in-one** (single process from the `tokenlive-standalone` repository running Gateway and Admin together). All-in-one MUST NOT run Gateway without Admin.

#### Scenario: Standalone uses mainline binaries
- **WHEN** operators choose standalone deployment
- **THEN** they run artifacts from `tokenlive-gateway` and/or `tokenlive-admin` without requiring the `tokenlive-standalone` binary

#### Scenario: All-in-one uses tokenlive-standalone
- **WHEN** operators choose all-in-one deployment
- **THEN** they run the `tokenlive-standalone` (or documented binary name) process which starts both LLM Gateway and full Admin surfaces in one process

#### Scenario: Reject gateway-only all-in-one
- **WHEN** configuration for `tokenlive-standalone` attempts to disable Admin while keeping only Gateway
- **THEN** startup MUST fail fast or the schema MUST make that combination impossible

### Requirement: tokenlive-standalone is the all-in-one assemble host
All-in-one process assembly (single listener, shared lifecycle, wiring of gateway library + admin library) SHALL be implemented in the `tokenlive-standalone` repository, not by copying Admin business code into the Gateway repository.

#### Scenario: Assemble from libraries
- **WHEN** `tokenlive-standalone` starts successfully
- **THEN** it has composed embeddable APIs from gateway and admin modules rather than reimplementing Engine or Admin CRUD

### Requirement: Single listener serves LLM, admin API, and console
In all-in-one mode, the process SHALL serve LLM proxy endpoints, management REST APIs, and the admin SPA on one primary HTTP listener.

#### Scenario: Unified listen
- **WHEN** `tokenlive-standalone` starts
- **THEN** one TCP port accepts `/v1/*`, `/api/v1/*`, and admin SPA assets

#### Scenario: LLM uses Gateway Engine
- **WHEN** a client sends `POST /v1/chat/completions` to the all-in-one process
- **THEN** the request is handled by the Gateway Engine pipeline hosted in-process

### Requirement: Atomic lifecycle
Gateway and Admin surfaces in all-in-one SHALL start and stop with the single `tokenlive-standalone` process.

#### Scenario: Health
- **WHEN** a client calls `GET /health` on all-in-one
- **THEN** success means the unified process HTTP server is up

#### Scenario: Shutdown
- **WHEN** the process shuts down
- **THEN** no orphan external admin or gateway child process remains
