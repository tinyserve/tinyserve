# tinyserve docs

Operational documentation for tinyserve.

## Contents

- [PREPARE_ENVIRONMENT.md](PREPARE_ENVIRONMENT.md) - Initial Mac mini setup (Docker, firewall, etc.)
- [GETTING_STARTED.md](GETTING_STARTED.md) - First-time tinyserve setup with Cloudflare Tunnel
- [ADD_NEW_SERVICE.md](ADD_NEW_SERVICE.md) - Adding and managing services
- [launchd/](launchd/) - LaunchAgent plist for running daemon as a user service

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   Cloudflare    │────▶│   cloudflared   │────▶│     Traefik     │
│     Tunnel      │     │   (container)   │     │   (container)   │
└─────────────────┘     └─────────────────┘     └────────┬────────┘
                                                         │
                                           ┌─────────────┼─────────────┐
                                           ▼             ▼             ▼
                                      ┌─────────┐  ┌─────────┐  ┌─────────┐
                                      │ Service │  │ Service │  │ Service │
                                      │    A    │  │    B    │  │    C    │
                                      └─────────┘  └─────────┘  └─────────┘
```

## Data locations

All runtime data is stored under `~/Library/Application Support/tinyserve/`:

- `state.db` - SQLite database with settings and service configurations
- `generated/current/` - Active docker-compose and config files
- `backups/` - Previous configurations (auto-pruned to last 10)
- `cloudflared/` - Tunnel credentials
- `logs/` - Daemon logs (future)

## API endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Daemon and container status |
| `/health` | GET | Proxy/tunnel health with error details |
| `/services` | GET | List all services |
| `/services` | POST | Add a new service |
| `/services/{name}` | DELETE | Remove a service |
| `/deploy` | POST | Generate config and restart containers |
| `/rollback` | POST | Restore previous configuration |
| `/logs?service=X` | GET | Get service logs |
| `/logs?service=X&follow=1` | GET | Stream logs in real-time |
| `/init` | POST | Initialize Cloudflare Tunnel |
