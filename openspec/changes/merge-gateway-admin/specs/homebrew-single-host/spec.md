## ADDED Requirements

### Requirement: Homebrew packages tokenlive-standalone
Homebrew packaging SHALL build and install the **`tokenlive-standalone`** all-in-one artifact. It MUST NOT replace documentation for standalone dual-process production deployment of `tokenlive-gateway` and `tokenlive-admin`.

#### Scenario: Formula installs all-in-one binary
- **WHEN** a user installs via the documented Homebrew Formula
- **THEN** the `tokenlive` (or documented) binary from `tokenlive-standalone` is on `PATH` and configured as all-in-one

#### Scenario: Production docs stay dual-process
- **WHEN** an operator follows production deployment docs
- **THEN** separate gateway and admin processes remain the recommended primary path

### Requirement: Brew defaults are full all-in-one
Default Homebrew config SHALL run all-in-one with embedded config and SQLite management data, without MySQL or an external Admin process.

#### Scenario: First start
- **WHEN** the service starts with brew defaults and empty data dir
- **THEN** SQLite is initialized and both LLM and admin surfaces are served

#### Scenario: Optional Redis
- **WHEN** Redis is unset in the brew template
- **THEN** memory state store is used and startup succeeds

### Requirement: Config and data paths
The Formula SHALL install a default all-in-one config and use a variable data directory outside Cellar for SQLite and logs.

#### Scenario: Template installed
- **WHEN** installation completes
- **THEN** default config exists at the documented prefix path

#### Scenario: Survive upgrade
- **WHEN** the Formula is upgraded
- **THEN** data dir contents remain available
