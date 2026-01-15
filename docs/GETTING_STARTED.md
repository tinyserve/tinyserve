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

## 2) Prevent sleep (critical for headless servers)

Mac mini will sleep when SSH sessions end, causing Cloudflare Tunnel and services to become unreachable. Disable sleep:

```bash
sudo pmset -a sleep 0
sudo pmset -a disksleep 0
sudo pmset -a displaysleep 0
sudo pmset -a powernap 0

# Verify settings
pmset -g
```

Alternatively, via System Settings:
- System Settings → Energy → set "Turn display off after" to "Never" (or a reasonable time).
- Uncheck "Prevent automatic sleeping when the display is off" if present.

## 3) Install Homebrew
```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```
- Follow the on-screen instructions to add Homebrew to your PATH.
- For Apple Silicon Macs, add to your shell profile (`~/.zprofile` or `~/.bash_profile`):
  ```bash
  eval "$(/opt/homebrew/bin/brew shellenv)"
  ```
- Verify: `brew --version`

## 4) Install a Docker runtime (no Docker Desktop)
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

**tinyserve checklist**: Run `tinyserve checklist` to verify Docker and other prerequisites.

## 5) Install tinyserve (Homebrew)
- If tinyserve is in Homebrew core: `brew install tinyserve`
- If using a tap: `brew tap tinyserve/tinyserve` then `brew install tinyserve`
- Verify: `tinyserve --help`

## 6) Tinyserve data root
- Create the data root (optional, tinyserve will create as needed):
  ```bash
  mkdir -p "~/Library/Application Support/tinyserve"/{generated,backups,logs,services,traefik,cloudflared}
  ```

## 7) Launch the daemon
- Foreground sanity check:
  - `TINYSERVE_API=http://127.0.0.1:7070 tinyserved`
  - In another terminal: `tinyserve status` should return JSON with `status: ok`.
- Install as LaunchAgent:
  - `tinyserve launchd install`
  - `tinyserve launchd status`

## 8) Provide Cloudflare access (recommended)
- Prepare a Cloudflare API token with Tunnel + DNS permissions for your domain.
- Run the one-shot init to create the tunnel and set defaults:
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

**Optional: direct public exposure (no Cloudflare Tunnel)**
- Point DNS A/AAAA records at your public IP.
- Forward ports 80/443 on your router to this Mac mini.
- Allow inbound 80/443 on your firewall.
- Use a reverse proxy (Traefik/Caddy/Nginx) to terminate TLS with Let's Encrypt.
- Use Dynamic DNS if your public IP changes.

## 9) Add a service
```bash
tinyserve service add \
  --name whoami \
  --image traefik/whoami:v1.10 \
  --port 80
```
- Since `--hostname` is omitted, tinyserve uses the default domain: `whoami.example.com`.
- Override with `--hostname custom.otherdomain.com` if needed.

## 10) Deploy
```bash
tinyserve deploy
```
- tinyserve stages configs, runs `docker compose up -d`, waits for health, and promotes on success.

## 11) Verify
- Status: `tinyserve status`
- Logs: `tinyserve logs --service whoami --tail 100`
- Browser: `https://whoami.example.com` (optionally behind Cloudflare Access).

## 12) Backups (recommended)
Set up S3-compatible backups before running production workloads. See `docs/BACKUP_RESTORE.md` for scripts, schedules, and restore steps.

## 13) Keep it running
- Install/update the launchd agent:
  - `tinyserve launchd install`
  - `tinyserve launchd status`
- Future updates: `brew upgrade tinyserve && tinyserve launchd uninstall && tinyserve launchd install`
- Add/remove services or redeploy via the CLI or (when available) the web UI.
