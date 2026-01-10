# Backup & Restore Guide

This document describes how to back up and restore tinyserve state and service data to/from S3-compatible storage.

## Overview

tinyserve's data lives under `~/Library/Application Support/tinyserve/` and includes:

| Component | Path | Description |
|-----------|------|-------------|
| **state.db** | `state.db` | SQLite database with settings, services, tunnel config |
| **state.db-wal** | `state.db-wal` | SQLite WAL (write-ahead log) for uncommitted changes |
| **generated/** | `generated/` | Active docker-compose.yml, Traefik, cloudflared configs |
| **backups/** | `backups/` | Local rollback snapshots (compose configs) |
| **services/** | `services/` | Per-service persistent data volumes |
| **traefik/** | `traefik/` | Traefik certificates and dynamic config |
| **cloudflared/** | `cloudflared/` | Tunnel credentials and config |
| **logs/** | `logs/` | Daemon logs (optional to back up) |

## Backup Types

### Full Backup

Includes everything needed to restore a complete tinyserve installation:
- SQLite database + WAL
- All generated configs
- Service data volumes
- Docker images (exported as tarballs)
- Traefik certificates
- Cloudflared credentials

**Use case**: Disaster recovery, migrating to new hardware.

### Partial Backup (State Only)

Includes only state and configuration, excludes large Docker images:
- SQLite database + WAL
- Generated configs
- Cloudflared credentials
- Traefik certificates

**Use case**: Daily backups, quick restores where images can be re-pulled.

### Service Data Backup

Backs up per-service persistent volumes only:
- `services/<service-name>/` directories
- Service-specific databases or files

**Use case**: Granular restore of individual service data.

## S3-Compatible Storage Setup

### Prerequisites

1. **S3 bucket** on any S3-compatible provider:
   - AWS S3
   - Cloudflare R2
   - MinIO
   - Backblaze B2
   - DigitalOcean Spaces

2. **Credentials** with read/write access:
   ```bash
   export AWS_ACCESS_KEY_ID="your-access-key"
   export AWS_SECRET_ACCESS_KEY="your-secret-key"
   export AWS_REGION="us-east-1"  # or your region
   export S3_ENDPOINT="https://s3.amazonaws.com"  # or provider endpoint
   ```

3. **AWS CLI** or compatible tool:
   ```bash
   brew install awscli
   ```

### Bucket Structure

```
s3://your-bucket/tinyserve-backups/
├── full/
│   ├── 2026-01-10T12-00-00Z/
│   │   ├── state.db
│   │   ├── state.db-wal
│   │   ├── configs.tar.gz
│   │   ├── services.tar.gz
│   │   ├── images.tar.gz
│   │   └── manifest.json
│   └── ...
├── partial/
│   ├── 2026-01-10T06-00-00Z/
│   │   ├── state.db
│   │   ├── state.db-wal
│   │   ├── configs.tar.gz
│   │   └── manifest.json
│   └── ...
└── wal/
    └── state.db-wal-2026-01-10T12-30-00Z  # continuous WAL shipping
```

## Backup Procedures

### Full Backup Script

Create `~/bin/tinyserve-backup-full.sh`:

```bash
#!/bin/bash
set -euo pipefail

TINYSERVE_ROOT="${HOME}/Library/Application Support/tinyserve"
BACKUP_BUCKET="${S3_BACKUP_BUCKET:-your-bucket}"
BACKUP_PREFIX="${S3_BACKUP_PREFIX:-tinyserve-backups}"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H-%M-%SZ")
BACKUP_PATH="${BACKUP_PREFIX}/full/${TIMESTAMP}"
WORK_DIR=$(mktemp -d)

cleanup() { rm -rf "${WORK_DIR}"; }
trap cleanup EXIT

echo "==> Creating full backup: ${TIMESTAMP}"

# 1. Stop daemon to ensure clean SQLite state
echo "Stopping tinyserved..."
launchctl unload ~/Library/LaunchAgents/dev.tinyserve.daemon.plist 2>/dev/null || true
sleep 2

# 2. Copy SQLite database (with WAL checkpoint)
echo "Backing up database..."
sqlite3 "${TINYSERVE_ROOT}/state.db" ".backup '${WORK_DIR}/state.db'"
cp "${TINYSERVE_ROOT}/state.db-wal" "${WORK_DIR}/state.db-wal" 2>/dev/null || true

# 3. Archive configs
echo "Archiving configs..."
tar -czf "${WORK_DIR}/configs.tar.gz" -C "${TINYSERVE_ROOT}" \
    generated backups traefik cloudflared 2>/dev/null || true

# 4. Archive service data
echo "Archiving service data..."
if [ -d "${TINYSERVE_ROOT}/services" ]; then
    tar -czf "${WORK_DIR}/services.tar.gz" -C "${TINYSERVE_ROOT}" services
fi

# 5. Export Docker images
echo "Exporting Docker images..."
COMPOSE_FILE="${TINYSERVE_ROOT}/generated/docker-compose.yml"
if [ -f "${COMPOSE_FILE}" ]; then
    IMAGES=$(grep -E '^\s+image:' "${COMPOSE_FILE}" | awk '{print $2}' | sort -u)
    if [ -n "${IMAGES}" ]; then
        docker save ${IMAGES} | gzip > "${WORK_DIR}/images.tar.gz"
    fi
fi

# 6. Create manifest
cat > "${WORK_DIR}/manifest.json" << EOF
{
    "timestamp": "${TIMESTAMP}",
    "type": "full",
    "hostname": "$(hostname)",
    "tinyserve_version": "$(tinyserve version 2>/dev/null || echo 'unknown')",
    "files": [
        "state.db",
        "configs.tar.gz",
        "services.tar.gz",
        "images.tar.gz"
    ]
}
EOF

# 7. Upload to S3
echo "Uploading to s3://${BACKUP_BUCKET}/${BACKUP_PATH}/"
aws s3 cp "${WORK_DIR}/" "s3://${BACKUP_BUCKET}/${BACKUP_PATH}/" \
    --recursive --quiet

# 8. Restart daemon
echo "Restarting tinyserved..."
launchctl load ~/Library/LaunchAgents/dev.tinyserve.daemon.plist

echo "==> Full backup complete: s3://${BACKUP_BUCKET}/${BACKUP_PATH}/"
```

### Partial Backup Script (No Images)

Create `~/bin/tinyserve-backup-partial.sh`:

```bash
#!/bin/bash
set -euo pipefail

TINYSERVE_ROOT="${HOME}/Library/Application Support/tinyserve"
BACKUP_BUCKET="${S3_BACKUP_BUCKET:-your-bucket}"
BACKUP_PREFIX="${S3_BACKUP_PREFIX:-tinyserve-backups}"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H-%M-%SZ")
BACKUP_PATH="${BACKUP_PREFIX}/partial/${TIMESTAMP}"
WORK_DIR=$(mktemp -d)

cleanup() { rm -rf "${WORK_DIR}"; }
trap cleanup EXIT

echo "==> Creating partial backup: ${TIMESTAMP}"

# Online backup using SQLite backup API (no daemon stop needed)
echo "Backing up database (online)..."
sqlite3 "${TINYSERVE_ROOT}/state.db" ".backup '${WORK_DIR}/state.db'"

# Archive configs
echo "Archiving configs..."
tar -czf "${WORK_DIR}/configs.tar.gz" -C "${TINYSERVE_ROOT}" \
    generated backups traefik cloudflared 2>/dev/null || true

# Create manifest
cat > "${WORK_DIR}/manifest.json" << EOF
{
    "timestamp": "${TIMESTAMP}",
    "type": "partial",
    "hostname": "$(hostname)",
    "tinyserve_version": "$(tinyserve version 2>/dev/null || echo 'unknown')"
}
EOF

# Upload
echo "Uploading to s3://${BACKUP_BUCKET}/${BACKUP_PATH}/"
aws s3 cp "${WORK_DIR}/" "s3://${BACKUP_BUCKET}/${BACKUP_PATH}/" \
    --recursive --quiet

echo "==> Partial backup complete: s3://${BACKUP_BUCKET}/${BACKUP_PATH}/"
```

### Continuous WAL Shipping (Near Real-Time)

For SQLite, enable continuous WAL backup for point-in-time recovery:

Create `~/bin/tinyserve-wal-ship.sh`:

```bash
#!/bin/bash
set -euo pipefail

TINYSERVE_ROOT="${HOME}/Library/Application Support/tinyserve"
BACKUP_BUCKET="${S3_BACKUP_BUCKET:-your-bucket}"
BACKUP_PREFIX="${S3_BACKUP_PREFIX:-tinyserve-backups}"
WAL_FILE="${TINYSERVE_ROOT}/state.db-wal"
LAST_SIZE_FILE="/tmp/tinyserve-wal-last-size"

# Only ship if WAL has grown
if [ -f "${WAL_FILE}" ]; then
    CURRENT_SIZE=$(stat -f%z "${WAL_FILE}" 2>/dev/null || echo 0)
    LAST_SIZE=$(cat "${LAST_SIZE_FILE}" 2>/dev/null || echo 0)
    
    if [ "${CURRENT_SIZE}" -gt "${LAST_SIZE}" ] && [ "${CURRENT_SIZE}" -gt 0 ]; then
        TIMESTAMP=$(date -u +"%Y-%m-%dT%H-%M-%SZ")
        
        # Copy WAL to temp location for upload
        cp "${WAL_FILE}" "/tmp/state.db-wal-${TIMESTAMP}"
        
        aws s3 cp "/tmp/state.db-wal-${TIMESTAMP}" \
            "s3://${BACKUP_BUCKET}/${BACKUP_PREFIX}/wal/state.db-wal-${TIMESTAMP}" \
            --quiet
        
        rm "/tmp/state.db-wal-${TIMESTAMP}"
        echo "${CURRENT_SIZE}" > "${LAST_SIZE_FILE}"
        
        echo "$(date): Shipped WAL (${CURRENT_SIZE} bytes)"
    fi
fi
```

Add to crontab for every 5 minutes:
```bash
*/5 * * * * ~/bin/tinyserve-wal-ship.sh >> ~/Library/Logs/tinyserve-wal-ship.log 2>&1
```

## Scheduled Backups

### Using cron

```bash
# Edit crontab
crontab -e

# Add schedules:
# Full backup weekly (Sunday 3am)
0 3 * * 0 ~/bin/tinyserve-backup-full.sh >> ~/Library/Logs/tinyserve-backup.log 2>&1

# Partial backup daily (3am)
0 3 * * 1-6 ~/bin/tinyserve-backup-partial.sh >> ~/Library/Logs/tinyserve-backup.log 2>&1

# WAL shipping every 5 minutes
*/5 * * * * ~/bin/tinyserve-wal-ship.sh >> ~/Library/Logs/tinyserve-wal-ship.log 2>&1
```

### Using launchd (recommended for macOS)

Create `~/Library/LaunchAgents/dev.tinyserve.backup.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>dev.tinyserve.backup</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>-c</string>
        <string>~/bin/tinyserve-backup-partial.sh</string>
    </array>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>3</integer>
        <key>Minute</key>
        <integer>0</integer>
    </dict>
    <key>StandardOutPath</key>
    <string>/Users/YOU/Library/Logs/tinyserve-backup.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/YOU/Library/Logs/tinyserve-backup.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>S3_BACKUP_BUCKET</key>
        <string>your-bucket</string>
        <key>AWS_ACCESS_KEY_ID</key>
        <string>your-key</string>
        <key>AWS_SECRET_ACCESS_KEY</key>
        <string>your-secret</string>
    </dict>
</dict>
</plist>
```

Load with:
```bash
launchctl load ~/Library/LaunchAgents/dev.tinyserve.backup.plist
```

## Restore Procedures

### List Available Backups

```bash
# List full backups
aws s3 ls "s3://${S3_BACKUP_BUCKET}/${S3_BACKUP_PREFIX}/full/"

# List partial backups
aws s3 ls "s3://${S3_BACKUP_BUCKET}/${S3_BACKUP_PREFIX}/partial/"
```

### Full Restore Script

Create `~/bin/tinyserve-restore.sh`:

```bash
#!/bin/bash
set -euo pipefail

TINYSERVE_ROOT="${HOME}/Library/Application Support/tinyserve"
BACKUP_BUCKET="${S3_BACKUP_BUCKET:-your-bucket}"
BACKUP_PREFIX="${S3_BACKUP_PREFIX:-tinyserve-backups}"
BACKUP_TIMESTAMP="${1:-}"
BACKUP_TYPE="${2:-full}"

if [ -z "${BACKUP_TIMESTAMP}" ]; then
    echo "Usage: $0 <timestamp> [full|partial]"
    echo "Example: $0 2026-01-10T12-00-00Z full"
    exit 1
fi

BACKUP_PATH="${BACKUP_PREFIX}/${BACKUP_TYPE}/${BACKUP_TIMESTAMP}"
WORK_DIR=$(mktemp -d)

cleanup() { rm -rf "${WORK_DIR}"; }
trap cleanup EXIT

echo "==> Restoring from s3://${BACKUP_BUCKET}/${BACKUP_PATH}/"

# 1. Stop daemon
echo "Stopping tinyserved..."
launchctl unload ~/Library/LaunchAgents/dev.tinyserve.daemon.plist 2>/dev/null || true
sleep 2

# 2. Download backup
echo "Downloading backup..."
aws s3 cp "s3://${BACKUP_BUCKET}/${BACKUP_PATH}/" "${WORK_DIR}/" --recursive

# 3. Verify manifest
if [ ! -f "${WORK_DIR}/manifest.json" ]; then
    echo "ERROR: No manifest.json found in backup"
    exit 1
fi
cat "${WORK_DIR}/manifest.json"

# 4. Back up current state (just in case)
if [ -f "${TINYSERVE_ROOT}/state.db" ]; then
    echo "Backing up current state..."
    mv "${TINYSERVE_ROOT}/state.db" "${TINYSERVE_ROOT}/state.db.pre-restore"
    mv "${TINYSERVE_ROOT}/state.db-wal" "${TINYSERVE_ROOT}/state.db-wal.pre-restore" 2>/dev/null || true
fi

# 5. Restore database
echo "Restoring database..."
cp "${WORK_DIR}/state.db" "${TINYSERVE_ROOT}/state.db"

# 6. Restore configs
if [ -f "${WORK_DIR}/configs.tar.gz" ]; then
    echo "Restoring configs..."
    tar -xzf "${WORK_DIR}/configs.tar.gz" -C "${TINYSERVE_ROOT}"
fi

# 7. Restore service data (if present)
if [ -f "${WORK_DIR}/services.tar.gz" ]; then
    echo "Restoring service data..."
    tar -xzf "${WORK_DIR}/services.tar.gz" -C "${TINYSERVE_ROOT}"
fi

# 8. Load Docker images (if present)
if [ -f "${WORK_DIR}/images.tar.gz" ]; then
    echo "Loading Docker images..."
    gunzip -c "${WORK_DIR}/images.tar.gz" | docker load
fi

# 9. Restart daemon
echo "Starting tinyserved..."
launchctl load ~/Library/LaunchAgents/dev.tinyserve.daemon.plist

# 10. Verify
sleep 3
tinyserve status

echo "==> Restore complete!"
```

### Partial Restore (State Only)

```bash
./tinyserve-restore.sh 2026-01-10T06-00-00Z partial
# Then re-deploy to pull images:
tinyserve deploy
```

### Point-in-Time Recovery with WAL

For recovering to a specific point using shipped WAL files:

```bash
# 1. Restore the last full/partial backup before your target time
./tinyserve-restore.sh 2026-01-10T00-00-00Z partial

# 2. Stop daemon
launchctl unload ~/Library/LaunchAgents/dev.tinyserve.daemon.plist

# 3. Download and apply WAL files in order
aws s3 cp "s3://${BUCKET}/tinyserve-backups/wal/state.db-wal-2026-01-10T00-05-00Z" \
    "~/Library/Application Support/tinyserve/state.db-wal"

# 4. Let SQLite replay the WAL on next open
launchctl load ~/Library/LaunchAgents/dev.tinyserve.daemon.plist
```

## Backup Retention

Add to your backup scripts to prune old backups:

```bash
# Keep last 7 days of partial backups
aws s3 ls "s3://${BUCKET}/${PREFIX}/partial/" | while read -r line; do
    TIMESTAMP=$(echo "$line" | awk '{print $NF}' | tr -d '/')
    if [[ "${TIMESTAMP}" < "$(date -v-7d -u +%Y-%m-%dT%H-%M-%SZ)" ]]; then
        aws s3 rm "s3://${BUCKET}/${PREFIX}/partial/${TIMESTAMP}/" --recursive
    fi
done

# Keep last 4 weeks of full backups
aws s3 ls "s3://${BUCKET}/${PREFIX}/full/" | while read -r line; do
    TIMESTAMP=$(echo "$line" | awk '{print $NF}' | tr -d '/')
    if [[ "${TIMESTAMP}" < "$(date -v-28d -u +%Y-%m-%dT%H-%M-%SZ)" ]]; then
        aws s3 rm "s3://${BUCKET}/${PREFIX}/full/${TIMESTAMP}/" --recursive
    fi
done

# Keep last 24 hours of WAL files
aws s3 ls "s3://${BUCKET}/${PREFIX}/wal/" | while read -r line; do
    FILE=$(echo "$line" | awk '{print $NF}')
    TIMESTAMP=$(echo "$FILE" | sed 's/state.db-wal-//')
    if [[ "${TIMESTAMP}" < "$(date -v-1d -u +%Y-%m-%dT%H-%M-%SZ)" ]]; then
        aws s3 rm "s3://${BUCKET}/${PREFIX}/wal/${FILE}"
    fi
done
```

## Future: Native CLI Integration

Planned CLI commands for backup/restore (not yet implemented):

```bash
# Configure S3 backup destination
tinyserve backup config --bucket my-bucket --prefix tinyserve-backups \
    --access-key KEY --secret-key SECRET --endpoint https://s3.amazonaws.com

# Manual backup
tinyserve backup create [--full | --partial]

# List backups
tinyserve backup list

# Restore
tinyserve backup restore <timestamp> [--full | --partial]

# Enable scheduled backups
tinyserve backup schedule --partial-daily --full-weekly --wal-continuous
```

## Checklist

Before going to production, verify:

- [ ] S3 bucket created with appropriate retention policy
- [ ] AWS credentials configured and tested (`aws s3 ls s3://your-bucket/`)
- [ ] Backup scripts installed in `~/bin/` and marked executable
- [ ] Test full backup runs successfully
- [ ] Test restore to a clean system works
- [ ] Scheduled backups configured (cron or launchd)
- [ ] WAL shipping enabled for near real-time recovery (if needed)
- [ ] Backup retention/pruning configured
- [ ] Backup alerts/monitoring configured (optional)
