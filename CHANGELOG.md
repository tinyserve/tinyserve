# Changelog

All notable changes to tinyserve will be documented in this file.

## [Unreleased]

### Added

- **SQLite persistence**: State is now stored in SQLite (`state.db`) instead of JSON.
  - Schema with `settings` and `services` tables
  - WAL mode for safe concurrent access
  - Automatic schema migrations
  - Validation on write

- **Streaming logs**: New `/logs?follow=1` API endpoint for real-time log streaming.
  - CLI: `tinyserve logs --service NAME --follow` (or `-f`)

- **Health endpoint**: New `/health` API endpoint exposing proxy/tunnel status with error details.

- **CLI error hints**: Connection errors now show helpful hints about starting the daemon.

- **Health polling after deploy**: Deployments now wait for services to become healthy before promoting the new configuration. Configurable timeout via `--timeout` flag (default 60s). Auto-rollback on health check failure.

- **`tinyserve init` command**: One-shot setup that creates a Cloudflare Tunnel via API and configures tinyserve.
  ```
  tinyserve init \
    --domain example.com \
    --cloudflare-api-token $TOKEN \
    --tunnel-name tinyserve-home \
    [--account-id ID]
  ```

- **Hostname collision detection**: Adding a service with a hostname already used by another service is now rejected with a clear error message.

- **Backup rotation**: Old backups are automatically pruned after successful deployments. Keeps the most recent N backups (configurable via `max_backups` in state, default 10).

- **Graceful shutdown**: The daemon now handles SIGINT/SIGTERM signals and gracefully drains in-flight requests (30s timeout) before exiting.

- **CLI improvements**:
  - `service list` - Lists all configured services in a table format
  - `service remove --name NAME` - Removes a service from configuration
  - `--volume` flag for `service add` - Mount host directories into containers
  - `--healthcheck` flag for `service add` - Define container health checks
  - `--follow` / `-f` flag for `logs` - Stream logs in real-time

- **Cloudflare API client** (`internal/cloudflare/`): New package for programmatic tunnel management (create, list, get token).

### Changed

- State storage switched from JSON file to SQLite database.
- Deploy response status changed from `"deploy_started"` to `"deployed"` to reflect that deployment now waits for health.
- `NewHandler()` now requires an additional `cloudflaredDir` parameter.

### Fixed

- Deployments no longer promote staging config immediately; they wait for health verification first.

### Testing

- Added unit tests for `internal/state` package (76% coverage)
  - State validation, InMemoryStore, FileStore with temp directories
  - SQLiteStore with schema migrations and concurrency tests
  - Concurrency safety tests with race detector
- Added unit tests for `internal/generate` package (93% coverage)
  - Name sanitization, hostname collection, Traefik label generation
  - Compose file generation with healthchecks, volumes, env vars, memory limits
- Added integration tests for `internal/api` package (30% coverage)
  - HTTP handler tests using `httptest` and `InMemoryStore`
  - Service add/list/delete, hostname collision detection
  - Backup pruning, method validation
