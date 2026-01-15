# Getting Started (Mac mini + first deploy)

This is the ideal end-to-end flow: prepare a clean Mac mini, install Docker + tinyserve, then initialize Cloudflare Tunnel and deploy your first service.

## 1) Base system & remote access (do this first, before going headless)

**System updates**:
- Update macOS to the latest stable release and reboot.
- Install Xcode Command Line Tools: `xcode-select --install` (accept the prompt).

**Create a dedicated admin user** (recommended):
- System Settings → Users & Groups → Add User.
- Choose "Administrator" role; pick a strong password.
- Log in as this user for all subsequent steps.

**Enable remote access**:
- System Settings → General → Sharing → enable **Remote Login** (SSH). Restrict to your admin user or group.
- Optionally enable **Screen Sharing** if you want GUI rescue.

**Note your machine's IP address and hostname**:
```bash
# Local hostname (usable as <hostname>.local on the same network)
scutil --get LocalHostName
```

## 2) Install Homebrew
```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```
- Follow the on-screen instructions to add Homebrew to your PATH.
- For Apple Silicon Macs, add to your shell profile (`~/.zprofile` or `~/.bash_profile`):
  ```bash
  eval "$(/opt/homebrew/bin/brew shellenv)"
  ```
- Verify: `brew --version`

## 3) Install a Docker runtime
Pick one:
- **Colima (recommended)**: `brew install colima docker` then `colima start --arch aarch64 --vm-type vz --cpu 4 --memory 8`
- **Rancher Desktop** with Docker API enabled (GUI option).
- (Advanced) **Lima** with Docker socket/nerdctl; ensure `docker` CLI points at the Lima socket.

**Ensure Docker daemon is running**:
```bash
docker info

docker ps
```

**Troubleshooting**:
- `Cannot connect to the Docker daemon`: the VM isn't running. Start Colima/Rancher.
- `docker: command not found`: install Docker CLI with `brew install docker`.
- `permission denied`: check that your user can access the Docker socket.

**Auto-start Colima on login** (optional):
```bash
brew services start colima
```

## 4) Install TinyServe (Homebrew) and run checklist

```bash
brew tap tinyserve/tinyserve
brew install tinyserve
brew service start docker
tinyserve checklist # Important to verify all prerequisites
```

## 5) Init TinyServe Provide Cloudflare access (recommended but optional)
- Prepare a Cloudflare API token with Tunnel + DNS permissions for your domain.
- Run interactive init to create the tunnel and set defaults:
  ```bash
  tinyserve init
  ```

- If you choose to use Cloudflare tunnerl, TinyServe will:
  - Create a Cloudflare Tunnel (or reuse if it exists).
  - Store tunnel credentials under `~/Library/Application Support/tinyserve/cloudflared/`.
  - Set `default_domain` and wire the tunnel to Traefik.
  - Verify Docker + compose availability.

**Optional: direct public exposure (no Cloudflare Tunnel)**
- Point DNS A/AAAA records at your public IP.
- Forward ports 80/443 on your router to this Mac mini.
- Allow inbound 80/443 on your firewall.
- Use a reverse proxy (Traefik/Caddy/Nginx) to terminate TLS with Let's Encrypt.
- Use Dynamic DNS if your public IP changes.

## 6) Enable remote access to dashboard UI and API (optional)

```bash
remote enable [--hostname H | --ui-hostname H] [--api-hostname H] [--cloudflare] [--deploy]
```

If you want UI and API to be accessible for internet, check REMOTE.md. 

## 7) Add a service and set up auto deploy from GitHub
```bash
tinyserve service add --image my_docker_image:v1.10
```
See ADD_NEW_SERVICE.md for more details.

## 8) Backups (recommended)
Set up S3-compatible backups before running production workloads. See `docs/BACKUP_RESTORE.md` for scripts, schedules, and restore steps.
