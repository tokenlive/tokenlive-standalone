# AGENTS.md

## Project

`tokenlive-standalone` is the **All-in-one** assemble repo for TokenLive.

- Depends on (Go module): `tokenlive-gateway`, `tokenlive-admin`
- Does **not** own Engine or Admin CRUD business logic
- Binary: `tokenlive` (planned)

## Deployment

Only two product modes:

1. **standalone dual-process** — gateway + admin repos (primary)
2. **all-in-one** — this repo only; Gateway and Admin both on

## OpenSpec

Active change: `openspec/changes/merge-gateway-admin/`

## Rules

- No `git commit` unless the user explicitly asks
- Prefer Chinese for user-facing communication when working with the owner
