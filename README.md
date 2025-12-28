# tinyserve

Local host manager for a single Mac mini running small Docker services. The daemon stays on `127.0.0.1`, fronts everything through Traefik and Cloudflare Tunnel, and keeps all generated state under `~/Library/Application Support/tinyserve/`.

## Project layout

- `cmd/tinyserved` — background daemon exposing the localhost REST API and serving the web UI.
- `cmd/tinyserve` — CLI that talks to the daemon over HTTP.
- `internal/state` — state model plus JSON file-backed store (swap with SQLite later if desired).
- `internal/generate` — compose/cloudflared/Traefik file generation (staging-first apply flow).
- `internal/docker` — thin wrapper around `docker compose` execution.
- `internal/api` — REST handlers for daemon endpoints.
- `webui/` — embedded static dashboard (status + services list pulled from the REST API).
- `docs/launchd/tinyserved.plist` — LaunchAgent example for user-level startup.
- `docs/README.md` — notes on the docs folder layout.

## Development quick start

```
go run ./cmd/tinyserved
```

In a second terminal, query the daemon via the CLI:

```
go run ./cmd/tinyserve status
```

Generated and runtime files live at `~/Library/Application Support/tinyserve/` (created on first run).

## CLI (current)

- `tinyserve status` — daemon + proxy/tunnel health snapshot.
- `tinyserve service add --name svc --image ghcr.io/user/svc:prod --hostname svc.example.com --port 8080 [--env K=V] [--mem 256]`
- `tinyserve deploy [--service NAME]` — regenerate compose config and `docker compose up -d`.
- `tinyserve logs --service NAME [--tail N]`
- `tinyserve rollback` — restore the last promoted compose config (best-effort).

## Next steps

- Switch the JSON store to SQLite-backed persistence and validation for service changes.
- Flesh out config generation to include user-defined services, safe apply/promotion, and backups.
- Harden deploy/rollback (state + config snapshots) and add Cloudflare credential validation.
