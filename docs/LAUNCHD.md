# Running tinyserved with launchd

This guide covers installing tinyserved as a macOS LaunchAgent so it starts automatically at login and restarts on failure.

## Prerequisites

- tinyserve installed (via Homebrew or manual build)
- Docker runtime running (Colima, Rancher Desktop, or Lima)

## Option A: Homebrew Services (Recommended for Homebrew users)

If you installed tinyserve via Homebrew, use `brew services` for the simplest setup:

```bash
brew services start tinyserve
```

This automatically:
- Creates and installs the launchd plist
- Starts the daemon immediately
- Configures it to start at login

Management commands:

```bash
# Check status
brew services info tinyserve

# Stop the service
brew services stop tinyserve

# Restart after upgrade
brew services restart tinyserve
```

Logs are written to `~/Library/Logs/Homebrew/tinyserved.log`.

## Option B: CLI Install

Use the CLI to install and configure the launchd agent with more control:

```bash
tinyserve launchd install
```

This will:
- Auto-detect the `tinyserved` binary location
- Generate the plist with correct paths
- Install to `~/Library/LaunchAgents/`
- Load and start the agent

Verify it's running:

```bash
tinyserve launchd status
tinyserve status
```

### Management Commands

```bash
# Check launchd agent status
tinyserve launchd status

# Uninstall the agent
tinyserve launchd uninstall

# Reinstall (e.g., after upgrading tinyserve)
tinyserve launchd uninstall
tinyserve launchd install
```

## Option C: Manual Installation

If you prefer manual setup or need customization:

### 1. Locate your tinyserved binary

```bash
which tinyserved
# Homebrew Apple Silicon: /opt/homebrew/bin/tinyserved
# Homebrew Intel: /usr/local/bin/tinyserved
```

### 2. Copy and configure the plist

```bash
mkdir -p ~/Library/LaunchAgents
cp docs/launchd/tinyserved.plist ~/Library/LaunchAgents/dev.tinyserve.daemon.plist
```

Edit the plist to update the binary path in `ProgramArguments`.

### 3. Load the LaunchAgent

```bash
launchctl load ~/Library/LaunchAgents/dev.tinyserve.daemon.plist
```

## launchctl Commands

For manual control beyond the CLI:

```bash
# Stop daemon (will auto-restart due to KeepAlive)
launchctl stop dev.tinyserve.daemon

# Start daemon
launchctl start dev.tinyserve.daemon

# Check detailed status
launchctl list dev.tinyserve.daemon
```

## Troubleshooting

### Daemon not starting

Check logs:

```bash
cat /tmp/tinyserved.log
cat /tmp/tinyserved.err
```

Common issues:

1. **Binary not found**: Verify the path in `ProgramArguments` exists and is executable
2. **Docker not available**: Ensure Colima/Rancher is running before loading the agent
3. **Port conflict**: Another process using port 7070

### Docker CLI not found

If you see "docker: command not found" in logs, add PATH to the plist:

```xml
<key>EnvironmentVariables</key>
<dict>
  <key>TINYSERVE_API</key>
  <string>http://127.0.0.1:7070</string>
  <key>PATH</key>
  <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
</dict>
```

### Permission denied

Ensure the binary is executable:

```bash
chmod +x /opt/homebrew/bin/tinyserved
```

### Check launchd errors

```bash
# View system log for launchd errors
log show --predicate 'subsystem == "com.apple.xpc.launchd"' --last 5m | grep tinyserve
```

## Plist Reference

| Key | Value | Description |
|-----|-------|-------------|
| `Label` | `dev.tinyserve.daemon` | Unique identifier for the job |
| `ProgramArguments` | `["/path/to/tinyserved"]` | Command to run |
| `RunAtLoad` | `true` | Start when plist is loaded |
| `KeepAlive.SuccessfulExit` | `false` | Restart if daemon crashes (non-zero exit) |
| `ThrottleInterval` | `10` | Wait 10s before restarting after crash |
| `StandardOutPath` | `/tmp/tinyserved.log` | Stdout log location |
| `StandardErrorPath` | `/tmp/tinyserved.err` | Stderr log location |

## Recovery

If tinyserve fails to start after a deploy or config change:

1. Stop the daemon: `launchctl stop dev.tinyserve.daemon`
2. Check generated configs: `ls ~/Library/Application\ Support/tinyserve/generated/`
3. Rollback if needed: `tinyserve rollback`
4. Start the daemon: `launchctl start dev.tinyserve.daemon`

For Docker Compose issues:

```bash
cd ~/Library/Application\ Support/tinyserve/generated/current
docker compose ps
docker compose logs
```

## Uninstallation

```bash
# Stop and unload
launchctl unload ~/Library/LaunchAgents/dev.tinyserve.daemon.plist

# Remove plist
rm ~/Library/LaunchAgents/dev.tinyserve.daemon.plist

# Optionally remove logs
