# Prepare Environment (Mac mini, first-time install)

An ideal, clean setup for running tinyserve on an Apple Silicon Mac mini via Homebrew distribution (no Go toolchain needed).

## 1) Base system
- Update macOS to the latest stable release and reboot.
- Create a dedicated admin user for ops (optional but recommended).
- Install Xcode Command Line Tools: `xcode-select --install` (accept the prompt).

## 2) Install a Docker runtime (no Docker Desktop)
Pick one:
- **Colima (recommended)**: `brew install colima docker` then `colima start --arch aarch64 --vm-type vz --cpu 4 --memory 8`
  - Verify daemon: `docker info`
  - Optional x86 images: add `--arch x86_64` when starting Colima (Rosetta required).
- **Rancher Desktop** with Docker API enabled (if you prefer a GUI).
- (Advanced) **Lima** with Docker socket/nerdctl; ensure `docker` CLI points at the Lima socket.

Notes:
- macOS cannot run the upstream Docker Engine natively; you need a VM-backed runtime like Colima/Rancher.
- Keep `docker` CLI installed (`brew install docker`) even when using Colima/Rancher; it talks to the socket they expose.

## 3) Install tinyserve (brew)
- If tinyserve is in Homebrew core: `brew install tinyserve`
- If using a tap: `brew tap tinyserve/tinyserve` then `brew install tinyserve`
- Ensure Homebrew’s `bin` is on `PATH` (e.g., `/opt/homebrew/bin` or `/usr/local/bin`).
- Verify: `tinyserve --help`

## 4) Tinyserve data root
- Create the data root:
  - `mkdir -p "~/Library/Application Support/tinyserve"/{generated,backups,logs,services,traefik,cloudflared}`
- Leave it empty; tinyserve will manage configs and state.

## 5) Launch daemon
- Foreground sanity check:
  - `TINYSERVE_API=http://127.0.0.1:7070 tinyserved`
  - In another terminal: `curl http://127.0.0.1:7070/status`
- Install as LaunchAgent:
  - Copy `docs/launchd/tinyserved.plist` to `~/Library/LaunchAgents/dev.tinyserve.daemon.plist`.
  - Update the binary path inside to the Homebrew install.
  - `launchctl load ~/Library/LaunchAgents/dev.tinyserve.daemon.plist`

## 6) Cloudflare & domain
- Tunnel creation and hostname wiring will be automated by tinyserve (see `docs/GETTING_STARTED.md`).
- Keep your Cloudflare API token or tunnel token handy for that step.

## 7) Firewall & remote access (headless-ready)
- macOS Application Firewall: leave it on; tinyserve binds to `127.0.0.1` only. No pf tweaks needed.
- Enable remote access before going headless:
  - System Settings → General → Sharing → enable **Remote Login** (SSH). Restrict to your admin user or group.
  - Optionally enable **Screen Sharing** if you want GUI rescue.
  - Set a DHCP reservation or static IP on your router for the Mac mini.
- Verify SSH works from another machine on the LAN: `ssh <user>@<mac-mini-ip>`.
- For off-site access, rely on Cloudflare Tunnel + Access rather than opening ports on your router.

Now you are ready to use tinyserve: the daemon is running on localhost and the CLI is installed and configured. 
