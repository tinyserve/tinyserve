# Getting Started (ideal flow)

This assumes you want to install tinyserve via Homebrew, have a Docker runtime running (Colima/Rancher/Lima), and want tinyserve to create/manage the Cloudflare Tunnel automatically.

## 0) Install tinyserve (Homebrew)
- If tinyserve is in Homebrew core: `brew install tinyserve`
- If using a tap: `brew tap tinyserve/tinyserve` then `brew install tinyserve`
- Verify: `tinyserve --help`

## 1) Launch the daemon
- If not already running via LaunchAgent, start it once in the foreground for a quick check:
  - `TINYSERVE_API=http://127.0.0.1:7070 tinyserved`
- Verify: `tinyserve status` should return JSON with `status: ok`.

## 2) Provide Cloudflare access
- Prepare a Cloudflare API token with Tunnel + DNS permissions for your domain.
- Run the (ideal) one-shot init to create the tunnel and set defaults:
  ```
  tinyserve init \
    --domain example.com \
    --cloudflare-api-token $CF_API_TOKEN \
    --tunnel-name tinyserve-home
  ```
- tinyserve will:
  - Create a Cloudflare Tunnel (or reuse if it exists).
  - Store tunnel credentials under `~/Library/Application Support/tinyserve/cloudflared/`.
  - Set `default_domain` and wire the tunnel to Traefik.
  - Verify Docker + compose availability.

## 2b) Optional: custom domain via reverse proxy + port forward (no Cloudflare Tunnel)
If you don't want Cloudflare Tunnel, you can expose tinyserve directly:
- Point DNS A/AAAA records at your public IP.
- Forward ports 80/443 on your router to this Mac mini.
- Allow inbound 80/443 on your firewall.
- Use a reverse proxy (Traefik/Caddy/Nginx) to terminate TLS with Let's Encrypt.
- Use Dynamic DNS if your public IP changes.

## 3) Add a service
- Use the CLI to register your first app (see `docs/ADD_NEW_SERVICE.md` for details):
  ```
  tinyserve service add \
    --name whoami \
    --image traefik/whoami:v1.10 \
    --hostname whoami.example.com \
    --port 80
  ```

## 4) Deploy
- Pull images and apply the stack:
  ```
  tinyserve deploy
  ```
- tinyserve stages configs, runs `docker compose up -d`, waits for health, and promotes on success.

## 5) Verify
- Status: `tinyserve status`
- Logs: `tinyserve logs --service whoami --tail 100`
- Browser: `https://whoami.example.com` (optionally behind Cloudflare Access).

## 6) Keep it running
- Install the launchd agent: `tinyserve launchd install`
- Check status: `tinyserve launchd status`
- Future updates: `brew upgrade tinyserve && tinyserve launchd uninstall && tinyserve launchd install`
- Add/remove services or redeploy via the CLI or (when available) the web UI.
