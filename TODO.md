# tinyserve TODO (MVP alignment)

## Completed

- [x] Apply/rollback: add health checks after `docker compose up -d`, promote only on success, and restore previous config/state on failure; keep backups pruned/rotated.
- [x] Cloudflare: `tinyserve init` command to create tunnel via API, store credentials, and configure state.
- [x] CLI: add commands for service list/remove, `--volume` and `--healthcheck` flags for service add.
- [x] Daemon: graceful shutdown on SIGINT/SIGTERM.
- [x] Validation: hostname collision detection when adding services.
- [x] Persistence: swap JSON store for SQLite (schema, migrations, validation on write), handle concurrent access safely.
- [x] Config generation: finalize compose/Traefik templates for all service fields (volumes, healthchecks, resources), ensure hostnames/labels are deterministic.
- [x] Daemon API: add `/logs?follow=1` for streaming logs, expose tunnel/proxy health with error surfaces.
- [x] CLI: add deploy status tracking, follow logs (`--follow`), and error hints when daemon is not running.
- [x] Testing: unit tests for state (76%), generate (93%), and API handlers (30%). Covers service management, hostname validation, backup pruning, compose generation, SQLite store.

## Remaining

- [ ] Web UI: add forms for add/deploy/rollback, live logs view, and proxy/tunnel/service health indicators with better empty/error states.
- [ ] Launchd/docs: include install/run instructions with plist path updates (binary location), add recovery notes for rollback and compose path.
- [ ] Docs: add full reverse-proxy + port-forward + firewall setup walkthrough for custom domains (non-Cloudflare Tunnel path).
- [ ] Deployment workflow: document GitHub Actions → registry → pull flow, tag conventions, and registry auth expectations.
- [ ] Observability: structured daemon logs, log file rotation under `~/Library/Application Support/tinyserve/logs/`.
- [ ] Testing: add tests for docker wrapper (mocking exec), cloudflare client (httptest), CLI flag parsing, and full deploy workflow integration tests.
- [ ] Backup/Restore: implement `tinyserve backup` subcommand with S3-compatible storage support:
  - `tinyserve backup config` — configure S3 bucket, credentials, endpoint.
  - `tinyserve backup create [--full | --partial]` — create and upload backup.
  - `tinyserve backup list` — list available backups from S3.
  - `tinyserve backup restore <timestamp>` — download and restore from S3.
  - `tinyserve backup schedule` — configure periodic backups via launchd.
  - WAL shipping for near real-time SQLite backup (continuous mode).
