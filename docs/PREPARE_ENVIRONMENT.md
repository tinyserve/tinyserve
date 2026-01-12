# Prepare Environment (Mac mini, first-time install)

An ideal, clean setup for running tinyserve on an Apple Silicon Mac mini via Homebrew distribution (no Go toolchain needed).

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

**Note your machine's IP address and hostname** (you'll need these to connect remotely):
```bash
# Local hostname (usable as <hostname>.local on the same network)
scutil --get LocalHostName

# IP address (Ethernet)
ipconfig getifaddr en0

# IP address (Wi-Fi, if applicable)
ipconfig getifaddr en1

# List all network interfaces with IPs
ifconfig | grep "inet "
```

**Network recommendations**:
- Set a DHCP reservation or static IP on your router for the Mac mini to keep the IP stable.
- macOS Application Firewall: leave it on; tinyserve binds to `127.0.0.1` only. No pf tweaks needed.
- Verify SSH works from another machine on the LAN: `ssh <user>@<mac-mini-ip>` (or `ssh <user>@<hostname>.local`).
- For off-site access, rely on Cloudflare Tunnel + Access rather than opening ports on your router.

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

## 3) Install a Docker runtime (no Docker Desktop)
Pick one:
- **Colima (recommended)**: `brew install colima docker` then `colima start --arch aarch64 --vm-type vz --cpu 4 --memory 8`
  - Verify daemon: `docker info`
  - Optional x86 images: add `--arch x86_64` when starting Colima (Rosetta required).
- **Rancher Desktop** with Docker API enabled (if you prefer a GUI).
- (Advanced) **Lima** with Docker socket/nerdctl; ensure `docker` CLI points at the Lima socket.

Notes:
- macOS cannot run the upstream Docker Engine natively; you need a VM-backed runtime like Colima/Rancher.
- Keep `docker` CLI installed (`brew install docker`) even when using Colima/Rancher; it talks to the socket they expose.

### Ensuring Docker daemon is running

Before using tinyserve, verify the Docker daemon is accessible:

```bash
# Check if Docker daemon is running
docker info

# If using Colima and it's not running:
colima status          # Check Colima VM status
colima start           # Start Colima (use same flags as initial setup)

# If using Rancher Desktop:
# Open Rancher Desktop app or check its menu bar icon

# Quick health check
docker ps              # Should list containers (empty list is OK)
```

**Troubleshooting**:
- `Cannot connect to the Docker daemon`: The VM isn't running. Start Colima/Rancher.
- `docker: command not found`: Install Docker CLI with `brew install docker`.
- `permission denied`: Check that your user can access the Docker socket.

**Auto-start Colima on login** (optional):
```bash
# Create a launchd agent for Colima
brew services start colima
```

**tinyserve checklist**: Run `tinyserve checklist` to verify Docker and all other prerequisites are properly configured.

## 4) Install tinyserve (brew)
- If tinyserve is in Homebrew core: `brew install tinyserve`
- If using a tap: `brew tap tinyserve/tinyserve` then `brew install tinyserve`
- Ensure Homebrew’s `bin` is on `PATH` (e.g., `/opt/homebrew/bin` or `/usr/local/bin`).
- Verify: `tinyserve --help`

## 5) Tinyserve data root
- Create the data root:
  - `mkdir -p "~/Library/Application Support/tinyserve"/{generated,backups,logs,services,traefik,cloudflared}`
- Leave it empty; tinyserve will manage configs and state.

## 6) Launch daemon
- Foreground sanity check:
  - `TINYSERVE_API=http://127.0.0.1:7070 tinyserved`
  - In another terminal: `curl http://127.0.0.1:7070/status`
- Install as LaunchAgent:
  - Copy `docs/launchd/tinyserved.plist` to `~/Library/LaunchAgents/dev.tinyserve.daemon.plist`.
  - Update the binary path inside to the Homebrew install.
  - `launchctl load ~/Library/LaunchAgents/dev.tinyserve.daemon.plist`

## 7) Backup Configuration (recommended)

Set up S3-compatible backup storage before running production workloads.

**Install AWS CLI**:
```bash
brew install awscli
```

**Configure credentials** (add to `~/.zshrc` or `~/.bash_profile`):
```bash
export AWS_ACCESS_KEY_ID="your-access-key"
export AWS_SECRET_ACCESS_KEY="your-secret-key"
export S3_BACKUP_BUCKET="your-bucket"
export S3_BACKUP_PREFIX="tinyserve-backups"
# For non-AWS providers:
# export AWS_ENDPOINT_URL="https://your-s3-endpoint.com"
```

**Verify S3 access**:
```bash
aws s3 ls s3://${S3_BACKUP_BUCKET}/
```

**Install backup scripts**:
```bash
mkdir -p ~/bin
# Copy scripts from docs/BACKUP_RESTORE.md or use tinyserve backup commands
chmod +x ~/bin/tinyserve-backup-*.sh
```

**Configure scheduled backups** (see `docs/BACKUP_RESTORE.md` for full details):
- Daily partial backups (state + configs, ~5 MB)
- Weekly full backups (includes Docker images)
- Optional: 5-minute WAL shipping for near real-time recovery

**Test backup/restore**:
```bash
# Create test backup
~/bin/tinyserve-backup-partial.sh

# Verify it uploaded
aws s3 ls "s3://${S3_BACKUP_BUCKET}/${S3_BACKUP_PREFIX}/partial/"
```

## 8) Cloudflare & domain
- Tunnel creation and hostname wiring will be automated by tinyserve (see `docs/GETTING_STARTED.md`).
- Keep your Cloudflare API token or tunnel token handy for that step.

## 8b) Optional: custom domain via reverse proxy + port forward (no Cloudflare Tunnel)
If you prefer to expose tinyserve directly to the internet with your own domain:
- **DNS**: point your domain (and any subdomains) to your public IP (A/AAAA records).
- **Router**: forward TCP ports **80** and **443** to this Mac mini.
- **Firewall**: allow inbound 80/443 (macOS Application Firewall or your edge firewall).
- **TLS**: run a reverse proxy with automatic certs (Traefik/Caddy/Nginx+Let’s Encrypt).
- **Dynamic IP**: if your ISP IP changes, use Dynamic DNS and keep DNS updated.

Now you are ready to use tinyserve: the daemon is running on localhost and the CLI is installed and configured. 
