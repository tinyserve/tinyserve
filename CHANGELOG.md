# Changelog

All notable changes to tinyserve will be documented in this file.

## [Unreleased]

### Added

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

- **Cloudflare API client** (`internal/cloudflare/`): New package for programmatic tunnel management (create, list, get token).

### Changed

- Deploy response status changed from `"deploy_started"` to `"deployed"` to reflect that deployment now waits for health.
- `NewHandler()` now requires an additional `cloudflaredDir` parameter.

### Fixed

- Deployments no longer promote staging config immediately; they wait for health verification first.
