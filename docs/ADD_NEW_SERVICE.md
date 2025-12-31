# Add & Deploy a New Service (ideal flow)

This assumes tinyserve is installed, the daemon is running on `127.0.0.1:7070`, Docker Desktop is up, and Cloudflare Tunnel credentials are already configured.

## 1) Choose service details
- Image: e.g., `ghcr.io/you/myapp:prod`
- Internal port inside the container: e.g., `8080`
- Memory limit: e.g., `256` MB
- Hostnames: e.g., `myapp.example.com`
- Optional env/volumes/healthcheck:
  - Env: `DATABASE_URL=...`
  - Volumes: `/Users/you/data/myapp:/data`
  - Healthcheck: `CMD curl -f http://localhost:8080/healthz`

## 2) Register the service
- CLI (ideal target flow):
  ```
  tinyserve service add \
    --name myapp \
    --image ghcr.io/you/myapp:prod \
    --hostname myapp.example.com \
    --port 8080 \
    --mem 256 \
    --env DATABASE_URL=postgres://... \
    --env LOG_LEVEL=info
  ```
- This writes the service into tinyserve state and generates Traefik labels + cloudflared ingress entries for the hostnames.

## 3) Deploy
- Pull image(s) and apply the stack:
  ```
  tinyserve deploy --service myapp   # or omit --service to deploy all
  ```
- The deploy flow stages configs, runs `docker compose up -d`, waits for health, then promotes to `generated/current/`. On failure, it restores the last-known-good config.

## 4) Verify
- Status:
  ```
  tinyserve status
  ```
  Check `service_count`, proxy/tunnel health, and updated timestamp.
- Logs (tail the service):
  ```
  tinyserve logs --service myapp --tail 200
  ```
- HTTP check:
  - Via Cloudflare Tunnel: `https://myapp.example.com` (behind Access if configured).
  - Local Traefik (if you’ve enabled local access): `curl -H "Host: myapp.example.com" http://127.0.0.1:80`.

## 5) Rollback (if needed)
- Restore previous compose bundle and rerun:
  ```
  tinyserve rollback
  ```
- Then re-verify status and logs.

## Notes and tips
- Keep image tags stable (e.g., `:prod` or release tags) and let CI push new versions.
- For sensitive env vars, prefer secrets management later; for now, store minimal secrets and limit permissions.
- If using Cloudflare Access, set policies on the hostnames you expose (e.g., allow your team emails only). 
