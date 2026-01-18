# Add & Deploy a New Service (ideal flow)

This assumes tinyserve is installed, the daemon is running on `127.0.0.1:7070`, Docker Desktop is up, and Cloudflare Tunnel credentials are already configured.

## 1) Choose service details
- Image: e.g., `ghcr.io/you/myapp:prod`
- Internal port inside the container: e.g., `8080`
- Memory limit: e.g., `256` MB
- Hostname (optional): e.g., `myapp.example.com`
  - If omitted, tinyserve uses `{service-name}.{default-domain}` automatically
  - The default domain is set during `tinyserve init --default-domain example.com`
- Optional env/volumes/healthcheck:
  - Env: `DATABASE_URL=...`
  - Volumes: `/Users/you/data/myapp:/data`
  - Healthcheck: `CMD curl -f http://localhost:8080/healthz`
  - If the image declares Dockerfile `VOLUME` paths and you don't pass `--volume`,
    tinyserve will auto-mount them under `~/Library/Application Support/tinyserve/services/<service>/...`
    (disable with `--no-auto-volumes`).

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

### Using `.env` files
- tinyserve does **not** auto-load `.env` files. Use `--env` flags or `tinyserve service edit` to set env vars in the service config.
- If your app expects a `.env` file on disk, keep it **outside** `generated/` (that folder is regenerated) and mount it as a volume.
- Recommended location: `~/Library/Application Support/tinyserve/services/<service>/.env` (or any stable path you manage).
- Example:
  ```bash
  tinyserve service add \
    --name myapp \
    --image ghcr.io/you/myapp:prod \
    --port 8080 \
    --volume "/Users/you/Library/Application Support/tinyserve/services/myapp/.env:/app/.env:ro"
  ```

## Default domain and hostname routing
All services are routed via hostname (not ports). Traefik handles routing based on the `Host` header, and Cloudflare Tunnel forwards traffic to Traefik.

- **With default domain set:** Services without `--hostname` get `{name}.{default-domain}`
- **With explicit hostname:** Use `--hostname` to override or use a different domain
- **Multiple hostnames:** The API supports multiple hostnames per service if needed

Example with default domain `mysite.io`:
```bash
tinyserve service add --name api --image myapi:latest --port 8080
# → accessible at https://api.mysite.io

tinyserve service add --name blog --image ghost:latest --port 2368 --hostname blog.example.com
# → accessible at https://blog.example.com (custom domain)
```

## Automated deployments with GitHub Actions

Set up a webhook to automatically deploy when your CI builds and pushes a new image.

### 1) Create a deploy token

```bash
# Token restricted to a specific service (recommended)
tinyserve remote token create --name github-ci --service myapp

# Or unrestricted token for all services
tinyserve remote token create --name github-ci
```

Save the token - it won't be shown again.

### 2) Add the token to GitHub secrets

In your repo: **Settings → Secrets and variables → Actions → New repository secret**

- Name: `TINYSERVE_DEPLOY_TOKEN`
- Value: `ts_...` (the token from step 1)

### 3) Add deploy step to your workflow

```yaml
# .github/workflows/deploy.yml
name: Build and Deploy

on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Build and push image
        run: |
          # Your build steps here
          docker build -t ghcr.io/you/myapp:latest .
          docker push ghcr.io/you/myapp:latest

      - name: Deploy to tinyserve
        run: |
          curl -X POST "https://api.yourserver.com/webhook/deploy/myapp" \
            -H "Authorization: Bearer ${{ secrets.TINYSERVE_DEPLOY_TOKEN }}" \
            --fail-with-body
```

### 4) Webhook details

**Endpoint:** `POST /webhook/deploy/{service-name}`

**Headers:** `Authorization: Bearer ts_...`

**Optional query params:**
- `?timeout=120` - health check timeout in seconds (default: 60)

**What it does:**
1. Validates the token and checks service authorization
2. Pulls the latest image from registry
3. Recreates the container with the new image
4. Waits for health check to pass
5. Auto-rollback if health check fails

**Example with timeout:**
```bash
curl -X POST "https://api.yourserver.com/webhook/deploy/myapp?timeout=120" \
  -H "Authorization: Bearer ${{ secrets.TINYSERVE_DEPLOY_TOKEN }}"
```

### Tips

- **Use mutable tags** like `:latest`, `:main`, or `:prod` for webhook deploys
- **Add a healthcheck** to your service for reliable deployments with auto-rollback
- **Restrict tokens** to specific services with `--service` for better security
- **Check logs** if deploy fails: `tinyserve logs --service myapp --tail 100`

## Editing a service

To change service configuration (image tag, ports, env vars, etc.):

```bash
# Opens your $EDITOR with the service config as JSON
tinyserve service edit --name myapp

# Edit and deploy in one step
tinyserve service edit --name myapp --deploy
```

Example: Change from fixed tag to `latest` for webhook deploys:
```json
{
  "name": "myapp",
  "image": "ghcr.io/you/myapp:latest",
  ...
}
```
