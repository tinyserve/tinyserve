# Remote Access

Expose the tinyserve dashboard and API over the internet for remote management and CI/CD webhooks.

## Overview

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  GitHub Actions │────▶│   Cloudflare    │────▶│   tinyserved    │
│  (webhook)      │     │   Tunnel        │     │   (:7070)       │
└─────────────────┘     └─────────────────┘     └─────────────────┘
                               │
┌─────────────────┐            │
│  Browser        │────────────┘
│  (dashboard)    │
└─────────────────┘
```

## Authentication

Two auth mechanisms for different use cases:

| Access Type | Who | Auth Method | Managed By |
|-------------|-----|-------------|------------|
| Dashboard + read API | Humans | Cloudflare Access (or alternatives) | Cloudflare / external |
| Mutating endpoints | CI/webhooks | Bearer token | tinyserve |

### Token Auth (built-in)

For CI/CD webhooks. Tokens are generated and stored by tinyserve.

Protected endpoints:
- `POST /deploy`
- `POST /rollback`
- `POST /services`
- `DELETE /services/{name}`
- `POST /init`

Usage:
```bash
curl -X POST \
  -H "Authorization: Bearer <token>" \
  https://admin.example.com/deploy
```

### Browser Auth (external)

For human access to the dashboard. Options:

#### Option 1: Cloudflare Access (recommended)

- Zero-trust access via Cloudflare's edge
- Auth methods: email OTP, Google, GitHub, SAML, etc.
- Set up via Cloudflare dashboard or API
- Pros: no server-side changes, SSO, audit logs
- Cons: requires Cloudflare

#### Option 2: Tailscale / Headscale

- Expose only on Tailscale network (100.x.x.x)
- No public exposure at all
- Pros: simple, private, no Cloudflare dependency
- Cons: requires Tailscale on all client devices

#### Option 3: HTTP Basic Auth (via reverse proxy)

- Add basic auth in Traefik/Caddy/nginx in front of tinyserve
- Pros: simple, no external dependencies
- Cons: less secure, password management

#### Option 4: OAuth2 Proxy

- Deploy oauth2-proxy as a sidecar
- Supports Google, GitHub, OIDC providers
- Pros: self-hosted SSO
- Cons: more complex setup

#### Option 5: VPN + No public exposure

- Access only via WireGuard/OpenVPN
- Pros: maximum security
- Cons: requires VPN client

## CLI Commands

```bash
# Enable remote access (adds route to Cloudflare Tunnel)
tinyserve remote enable --hostname admin.example.com

# Disable remote access (removes route)
tinyserve remote disable

# Manage deploy tokens
tinyserve remote token create [--name "github-actions"]
tinyserve remote token list
tinyserve remote token revoke <token-id>
```

## Setup Flow

### 1. Enable remote access

```bash
tinyserve remote enable --hostname admin.example.com
```

This will:
- Add `admin.example.com → localhost:7070` to cloudflared config
- Create DNS record via Cloudflare API
- Redeploy cloudflared container

### 2. Create deploy token

```bash
tinyserve remote token create --name "github-actions"
# Output: Token created: ts_abc123...
# Store this securely - it won't be shown again
```

### 3. Configure browser auth (optional but recommended)

For Cloudflare Access:
1. Go to Cloudflare Zero Trust dashboard
2. Create an Access Application for `admin.example.com`
3. Add authentication policy (e.g., allow specific emails)

Or use one of the alternative options above.

### 4. Set up GitHub Actions

```yaml
# .github/workflows/deploy.yml
name: Deploy
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
          docker build -t ghcr.io/${{ github.repository }}:${{ github.sha }} .
          docker push ghcr.io/${{ github.repository }}:${{ github.sha }}
      
      - name: Deploy to tinyserve
        run: |
          curl -X POST \
            -H "Authorization: Bearer ${{ secrets.TINYSERVE_DEPLOY_TOKEN }}" \
            -H "Content-Type: application/json" \
            -d '{"service": "myapp"}' \
            https://admin.example.com/deploy
```

Add `TINYSERVE_DEPLOY_TOKEN` to your repository secrets.

## Security Considerations

1. **Token rotation**: Periodically revoke and recreate tokens
2. **Least privilege**: Create separate tokens per repo/workflow
3. **HTTPS only**: Cloudflare Tunnel enforces this by default
4. **Rate limiting**: Consider adding rate limits to /deploy (future)
5. **Audit log**: Deploy events are logged (future: structured logs)

## Troubleshooting

### Token rejected

```bash
# List tokens to verify it exists
tinyserve remote token list

# Create a new token if needed
tinyserve remote token create --name "new-token"
```

### Cannot reach admin hostname

```bash
# Check tunnel status
tinyserve status

# Verify cloudflared config includes the API route
cat ~/Library/Application\ Support/tinyserve/generated/current/cloudflared.yml
```

### Cloudflare Access blocking webhooks

If using Cloudflare Access, create a bypass policy:
1. Access → Applications → your app → Policies
2. Add policy: "Bypass" for "Service Token"
3. Create a Cloudflare Service Token for CI

Or: Don't enable Access on the admin hostname, rely only on tinyserve token auth.
