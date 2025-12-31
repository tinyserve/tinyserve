# tinyserve TODO (MVP alignment)

## Completed

- [x] Apply/rollback: add health checks after `docker compose up -d`, promote only on success, and restore previous config/state on failure; keep backups pruned/rotated.
- [x] Cloudflare: `tinyserve init` command to create tunnel via API, store credentials, and configure state.
- [x] CLI: add commands for service list/remove, `--volume` and `--healthcheck` flags for service add.
- [x] Daemon: graceful shutdown on SIGINT/SIGTERM.
- [x] Validation: hostname collision detection when adding services.

## In Progress / Remaining

- [ ] Persistence: swap JSON store for SQLite (schema, migrations, validation on write), handle concurrent access safely.
- [ ] Config generation: finalize compose/Traefik templates for all service fields (volumes, healthchecks, resources), ensure hostnames/labels are deterministic.
- [ ] Daemon API: add `/logs?follow=1` for streaming logs, expose tunnel/proxy health with error surfaces.
- [ ] CLI: add deploy status tracking, follow logs (`--follow`), and error hints when daemon is not running.
- [ ] Web UI: add forms for add/deploy/rollback, live logs view, and proxy/tunnel/service health indicators with better empty/error states.
- [ ] Launchd/docs: include install/run instructions with plist path updates (binary location), add recovery notes for rollback and compose path.
- [ ] Deployment workflow: document GitHub Actions → registry → pull flow, tag conventions, and registry auth expectations.
- [ ] Observability: structured daemon logs, log file rotation under `~/Library/Application Support/tinyserve/logs/`.
- [x] Testing: unit tests for state (76%), generate (93%), and API handlers (30%). Covers service management, hostname validation, backup pruning, compose generation.
- [ ] Testing: add tests for docker wrapper (mocking exec), cloudflare client (httptest), CLI flag parsing, and full deploy workflow integration tests.
