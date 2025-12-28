# tinyserve TODO (MVP alignment)

- [ ] Persistence: swap JSON store for SQLite (schema, migrations, validation on write), handle concurrent access safely.
- [ ] Config generation: finalize compose/Traefik templates for all service fields (volumes, healthchecks, resources), ensure hostnames/labels are deterministic, and emit cloudflared config using user-provided token/credentials.
- [ ] Apply/rollback: add health checks after `docker compose up -d`, promote only on success, and restore previous config/state on failure; keep backups pruned/rotated.
- [ ] Daemon API: complete `/deploy` semantics (per-service vs all, status reporting), add `/logs?follow=1`, and expose tunnel/proxy health with error surfaces.
- [ ] CLI: add commands for deploy status, follow logs, service list/remove, and error hints when daemon is not running.
- [ ] Web UI: add forms for add/deploy/rollback, live logs view, and proxy/tunnel/service health indicators with better empty/error states.
- [ ] Cloudflare: document how to supply tunnel token/creds, validate presence on `init`, and error clearly when missing.
- [ ] Launchd/docs: include install/run instructions with plist path updates (binary location), add recovery notes for rollback and compose path.
- [ ] Deployment workflow: document GitHub Actions → registry → pull flow, tag conventions, and registry auth expectations.
- [ ] Observability: structured daemon logs, log file rotation under `~/Library/Application Support/tinyserve/logs/`.
- [ ] Testing: add unit tests for state validation, generator output, API handlers, and CLI flag parsing; consider integration test for compose generation.
