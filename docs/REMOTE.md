# Remote Access

Expose the tinyserve dashboard and webhook API over the internet for remote management and CI/CD.

## Overview

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  GitHub Actions │────▶│   Cloudflare    │────▶│   tinyserved    │
│  (webhook)      │     │   Tunnel        │     │   (:7072)       │
└─────────────────┘     └─────────────────┘     └─────────────────┘
                               │
┌─────────────────┐            │
│  Browser        │────────────┘
│  (dashboard)    │              tinyserved (:7071)
└─────────────────┘
```

## Authentication

Two auth mechanisms for different use cases:

| Access Type | Who | Auth Method | Managed By |
|-------------|-----|-------------|------------|
| Dashboard + read API | Humans | Cloudflare Access (or alternatives) | Cloudflare / external |
| Webhook endpoints | CI/webhooks | Bearer token | tinyserve |

### Token Auth (built-in)

For CI/CD webhooks. Tokens are generated and stored by tinyserve.

Protected endpoint:
- `POST /webhook/deploy/{service}`

Usage:
```bash
curl -X POST \
  -H "Authorization: Bearer <token>" \
  https://api.example.com/webhook/deploy/myapp
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
# Enable remote access (adds routes to Cloudflare Tunnel)
tinyserve remote enable --ui-hostname ui.example.com --api-hostname api.example.com --cloudflare

# Disable remote access (removes route)
tinyserve remote disable

# Manage deploy tokens
tinyserve remote token create [--name "github-actions"]
tinyserve remote token list
tinyserve remote token revoke <token-id>

# Configure browser authentication
tinyserve remote auth cloudflare-access --team-domain <team>.cloudflareaccess.com --policy-aud <aud>
tinyserve remote auth disable
```

## Setup Flow

### 1. Enable remote access (with Cloudflare)

```bash
tinyserve remote enable --ui-hostname ui.example.com --api-hostname api.example.com --cloudflare
```

This automatically:
- Creates DNS CNAME records (`ui.example.com` and `api.example.com` → `<tunnel-id>.cfargotunnel.com`) via Cloudflare API
- Adds both hostnames to cloudflared ingress config
- Restarts the cloudflared container with the new config

**Note:** Requires `tinyserve init` to have been run first to configure the Cloudflare tunnel.

### 2. Create deploy token (for webhook)

```bash
tinyserve remote token create --name "github-actions"
# Output: Token created: ts_abc123...
# Store this securely - it won't be shown again
```

### 3. Configure browser auth (recommended)

Without authentication, anyone with your UI URL can access the dashboard. Cloudflare Access provides authentication at the edge before requests reach your server.

**Note:** You do NOT need the WARP client. Cloudflare Access works through the browser - it redirects to a login page and sets a JWT cookie after authentication.

#### Step 1: Access Zero Trust Dashboard

Go to https://one.dash.cloudflare.com/

If you haven't set up Zero Trust before, you'll need to create an organization (free for up to 50 users).

#### Step 2: Find your Team Domain

Your team domain is shown in the Zero Trust dashboard:
- Look at the URL or go to **Settings → General → Team domain**
- Format: `<team-name>.cloudflareaccess.com`

#### Step 3: Create an Access Application

1. Go to **Access → Applications → Add an application**
2. Choose **Self-hosted**
3. Configure the application:
   - **Application name**: `tinyserve` (or any name you prefer)
   - **Session Duration**: 24 hours (adjust as needed)
   - **Application domain**: your UI hostname (e.g., `tinyserve.example.com`)
4. Click **Next**

#### Step 4: Create an Access Policy

1. **Policy name**: `Allow me` (or descriptive name)
2. **Action**: `Allow`
3. **Configure rules** → Include:
   - **Selector**: `Emails`
   - **Value**: your email address (e.g., `you@example.com`)
4. Click **Next** → **Add application**

You can add multiple rules (other emails, email domains, identity providers like Google/GitHub, etc.)

#### Step 5: Get the Application Audience (AUD)

After creating the application:
1. Go to **Access → Applications**
2. Click on your application name
3. Find **Application Audience (AUD) Tag** in the overview - it's a long string like `32eafc7a8e7b123...`
4. Copy this value

#### Step 6: Configure tinyserve

```bash
tinyserve remote auth cloudflare-access \
  --team-domain <team-name>.cloudflareaccess.com \
  --policy-aud <aud-tag-from-step-5>
```

Example:
```bash
tinyserve remote auth cloudflare-access \
  --team-domain mycompany.cloudflareaccess.com \
  --policy-aud 32eafc7a8e7b4f2a9c1d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b
```

#### Verify it works

1. Open your UI hostname in a browser (use incognito to avoid cached sessions)
2. You should be redirected to a Cloudflare login page
3. After authenticating, you'll be redirected to the tinyserve UI

#### Disable authentication (not recommended)

```bash
tinyserve remote auth disable
```

**Warning:** This makes your UI publicly accessible to anyone with the URL.

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
            https://api.example.com/webhook/deploy/myapp
```

Add `TINYSERVE_DEPLOY_TOKEN` to your repository secrets.

## Security Considerations

1. **Token rotation**: Periodically revoke and recreate tokens
2. **Least privilege**: Create separate tokens per repo/workflow
3. **HTTPS only**: Cloudflare Tunnel enforces this by default
4. **Rate limiting**: Consider adding rate limits to /webhook/deploy (future)
5. **Audit log**: Deploy events are logged (future: structured logs)

## Troubleshooting

### Token rejected

```bash
# List tokens to verify it exists
tinyserve remote token list

# Create a new token if needed
tinyserve remote token create --name "new-token"
```

### Cannot reach UI or API hostname

```bash
# Check tunnel status
tinyserve status

# Verify cloudflared config includes both routes
cat ~/Library/Application\ Support/tinyserve/generated/current/cloudflared.yml
```

### Cloudflare Access blocking webhooks

If using Cloudflare Access, create a bypass policy:
1. Access → Applications → your app → Policies
2. Add policy: "Bypass" for "Service Token"
3. Create a Cloudflare Service Token for CI

Or: Don't enable Access on the API hostname, rely only on tinyserve token auth.
