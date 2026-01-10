# Remote Daemon Mode Design

This document explores how tinyserve CLI can securely connect to a remote tinyserved daemon over SSH or HTTPS.

## Current Architecture

```
┌──────────────┐      HTTP (localhost)     ┌──────────────┐
│  tinyserve   │ ───────────────────────▶  │  tinyserved  │
│    (CLI)     │     127.0.0.1:7070        │   (daemon)   │
└──────────────┘                           └──────────────┘
                                                  │
                                                  ▼
                                           Docker / Cloudflare
```

- Daemon binds to `127.0.0.1:7070` (localhost only)
- No authentication—anyone with localhost access can control everything
- CLI reads `TINYSERVE_API` env var to override the default address

---

## Goals

1. Allow CLI on machine A to control daemon on machine B
2. Maintain security: authentication, encryption, authorization
3. Minimal configuration burden
4. Support both ad-hoc (SSH tunnel) and persistent (HTTPS) modes

---

## Two Proposed Modes

### Mode 1: SSH Tunnel (Recommended for simplicity)

The CLI establishes an SSH tunnel to the remote host, forwarding a local port to the daemon's localhost port.

```
┌──────────────┐                              ┌──────────────────┐
│  tinyserve   │ ──SSH tunnel──────────────▶  │  remote host     │
│    (CLI)     │  localhost:17070 ────────▶   │  127.0.0.1:7070  │
└──────────────┘                              │   (tinyserved)   │
                                              └──────────────────┘
```

**CLI invocation options:**

```bash
# Option A: Explicit SSH tunnel mode
tinyserve --remote user@host status

# Option B: Use existing tunnel manually
ssh -L 17070:127.0.0.1:7070 user@host -N &
TINYSERVE_API=http://127.0.0.1:17070 tinyserve status
```

**Advantages:**
- Leverages existing SSH infrastructure (keys, known_hosts, jump hosts)
- No changes to daemon—it stays localhost-only
- No new authentication mechanism needed
- Works through firewalls/NAT
- Encrypted by default

**Disadvantages:**
- Requires SSH access to remote host
- Connection overhead per command (unless tunneled persistently)
- Not suitable for multi-tenant / shared daemon access

---

### Mode 2: HTTPS with API Token Authentication

Daemon listens on 0.0.0.0 (or a specific interface) over HTTPS with token-based auth.

```
┌──────────────┐       HTTPS + Bearer Token      ┌──────────────┐
│  tinyserve   │ ─────────────────────────────▶  │  tinyserved  │
│    (CLI)     │      https://host:7070          │   (daemon)   │
└──────────────┘                                 └──────────────┘
```

**Advantages:**
- Direct connection, lower latency
- Supports multiple clients without SSH access
- Can integrate with reverse proxies, load balancers
- Easier programmatic access (scripts, CI/CD)

**Disadvantages:**
- Requires TLS certificate management
- Requires token generation, storage, and rotation
- Exposes attack surface to the network
- More complex configuration

---

## Security Analysis

### Threat Model

| Threat | SSH Mode | HTTPS Mode |
|--------|----------|------------|
| **Eavesdropping** | ✅ SSH encrypted | ✅ TLS encrypted |
| **MITM attack** | ✅ SSH host key verification | ⚠️ Requires proper TLS cert validation |
| **Unauthorized access** | ✅ SSH auth (keys/password) | ⚠️ Depends on token strength/storage |
| **Replay attacks** | ✅ SSH handles | ⚠️ Need token expiry or nonces |
| **Credential theft** | ⚠️ SSH key on disk | ⚠️ Token on disk/env |
| **Brute force** | ✅ SSH rate limiting | ⚠️ Need rate limiting |
| **Privilege escalation** | N/A (SSH user perms) | ⚠️ Token grants full access |

### SSH Mode Security Considerations

1. **Host key verification**: CLI should respect `~/.ssh/known_hosts`
2. **Agent forwarding**: Not needed, avoid using it
3. **Key management**: Use existing SSH key infrastructure
4. **Timeout**: SSH connection should timeout if idle too long
5. **Audit**: SSH access logged on remote host

### HTTPS Mode Security Considerations

1. **TLS Certificates**
   - Self-signed: Requires distributing CA or disabling verification (risky)
   - Let's Encrypt: Requires domain + port 80/443 exposure
   - Private CA: Good for internal use, needs CA distribution

2. **API Token Design**
   ```
   Token format: ts_<random-256-bit-hex>
   Example: ts_a1b2c3d4e5f6...
   
   Storage on daemon: bcrypt hash in state.db
   Storage on client: ~/.config/tinyserve/credentials or env var
   ```

3. **Token Lifecycle**
   - Generate: `tinyserved token generate --name "laptop"`
   - List: `tinyserved token list`
   - Revoke: `tinyserved token revoke --name "laptop"`
   - Expiry: Optional TTL (e.g., 90 days)

4. **Rate Limiting**
   - Max 10 failed auth attempts per IP per minute
   - Exponential backoff after failures

5. **Authorization (future)**
   - Currently: Token = full admin access
   - Future: Role-based (read-only, deploy-only, admin)

---

## Attack Scenarios & Mitigations

### Scenario 1: Stolen API Token

**Risk**: Attacker with token has full daemon control.

**Mitigations**:
- Token revocation via daemon CLI
- Short-lived tokens with refresh mechanism
- IP allowlist per token (optional)
- Audit log of all API calls with token name

### Scenario 2: Exposed Daemon Port

**Risk**: Daemon on 0.0.0.0 accessible from internet.

**Mitigations**:
- Firewall rules (allow specific IPs only)
- Bind to private interface only
- Require VPN for access
- Fail2ban-style blocking after auth failures

### Scenario 3: Compromised TLS Certificate

**Risk**: MITM if cert private key leaked.

**Mitigations**:
- Certificate rotation
- Short-lived certs (ACME)
- Monitor for unauthorized cert issuance

### Scenario 4: SSH Key Compromise

**Risk**: Attacker with SSH key can tunnel to daemon.

**Mitigations**:
- Passphrase-protected SSH keys
- Hardware keys (YubiKey)
- SSH key rotation
- Limit SSH user permissions on remote host

---

## Configuration Design

### CLI Configuration (`~/.config/tinyserve/config.toml`)

```toml
# Default context
default_context = "local"

[contexts.local]
api = "http://127.0.0.1:7070"

[contexts.production]
mode = "ssh"
host = "user@prod.example.com"
# Uses SSH to tunnel, daemon stays on localhost

[contexts.staging]
mode = "https"
api = "https://staging.example.com:7070"
token_env = "TINYSERVE_STAGING_TOKEN"  # Read from env
# Or: token_file = "~/.config/tinyserve/tokens/staging"

[contexts.dev]
mode = "https"
api = "https://10.0.1.50:7070"
token = "ts_abc123..."  # Inline (not recommended)
tls_ca = "~/.config/tinyserve/ca.pem"  # Custom CA
```

### CLI Usage

```bash
# Use default context
tinyserve status

# Use specific context
tinyserve --context production status
tinyserve -c staging deploy --service myapp

# Override with env
TINYSERVE_CONTEXT=production tinyserve status

# Ad-hoc remote (SSH mode)
tinyserve --remote user@host status
```

### Daemon Configuration (`tinyserved.toml` or flags)

```toml
# Binding
listen = "127.0.0.1:7070"  # Default: localhost only
# listen = "0.0.0.0:7070"  # Network-accessible (requires auth)

# TLS (required if listen is not localhost)
[tls]
cert = "/path/to/cert.pem"
key = "/path/to/key.pem"
# Or: acme_email = "admin@example.com" for auto Let's Encrypt

# Authentication (required if listen is not localhost)
[auth]
required = true
tokens_db = "~/.config/tinyserve/tokens.db"  # Or use main state.db
```

---

## Implementation Plan

### Phase 1: SSH Tunnel Mode (Low effort, high value)

1. Add `--remote` flag to CLI
2. CLI spawns SSH tunnel: `ssh -L <local>:127.0.0.1:7070 <host> -N`
3. Execute command against tunneled port
4. Tear down tunnel after command

**Changes:**
- `cmd/tinyserve/main.go`: Add `--remote` flag parsing
- `cmd/tinyserve/remote.go`: SSH tunnel management
- No daemon changes

### Phase 2: Context/Profile Support

1. Add `~/.config/tinyserve/config.toml` support
2. Add `--context` flag
3. Support switching between local/remote daemons

### Phase 3: HTTPS + Token Auth

1. **Daemon changes:**
   - Optional TLS listener
   - Token auth middleware
   - Token management commands (`tinyserved token ...`)
   - Rate limiting middleware

2. **CLI changes:**
   - Send `Authorization: Bearer <token>` header
   - Token storage/retrieval from config

3. **New files:**
   - `internal/auth/token.go`: Token generation, hashing, validation
   - `internal/api/middleware.go`: Auth middleware
   - `internal/api/tls.go`: TLS configuration

### Phase 4: Hardening

1. Audit logging
2. IP allowlists
3. Token expiry/rotation
4. Role-based permissions

---

## Open Questions

1. **SSH tunnel persistence**: Should CLI keep tunnel open for multiple commands? (Use control socket?)

2. **Token storage**: Keychain integration vs file-based? (macOS Keychain, Linux secret-service)

3. **Multi-tenant**: Do we need multiple users with different permissions? Or single-admin model?

4. **Daemon discovery**: mDNS/Bonjour for local network daemon discovery?

5. **WebUI access**: How to handle browser access over SSH tunnel? (Separate concern?)

6. **Mutual TLS**: Should clients also present certificates? (Higher security, more complexity)

---

## Recommendation

**Start with SSH tunnel mode (Phase 1)**

Rationale:
- Zero daemon changes required
- Leverages battle-tested SSH security
- Works immediately for users with SSH access
- Covers 90% of use cases (single admin managing their own servers)
- Can add HTTPS mode later for advanced use cases

SSH mode command example:
```bash
# Simple remote command
tinyserve --remote deploy@myserver.com status

# With custom SSH options
tinyserve --remote deploy@myserver.com --ssh-opts="-i ~/.ssh/deploy_key" status
```

---

## Appendix: SSH Tunnel Implementation Sketch

```go
func runWithSSHTunnel(host string, sshOpts []string, fn func(apiBase string) error) error {
    // Find free local port
    listener, _ := net.Listen("tcp", "127.0.0.1:0")
    localPort := listener.Addr().(*net.TCPAddr).Port
    listener.Close()
    
    // Build SSH command
    args := []string{
        "-L", fmt.Sprintf("%d:127.0.0.1:7070", localPort),
        "-N",  // No command
        "-o", "ExitOnForwardFailure=yes",
        "-o", "ConnectTimeout=10",
    }
    args = append(args, sshOpts...)
    args = append(args, host)
    
    cmd := exec.Command("ssh", args...)
    cmd.Stderr = os.Stderr
    
    if err := cmd.Start(); err != nil {
        return fmt.Errorf("ssh tunnel: %w", err)
    }
    defer cmd.Process.Kill()
    
    // Wait for tunnel to be ready
    apiBase := fmt.Sprintf("http://127.0.0.1:%d", localPort)
    if err := waitForReady(apiBase, 10*time.Second); err != nil {
        return fmt.Errorf("tunnel not ready: %w", err)
    }
    
    return fn(apiBase)
}
```

---

## References

- [SSH Port Forwarding](https://www.ssh.com/academy/ssh/tunneling-example)
- [Go crypto/tls](https://pkg.go.dev/crypto/tls)
- [OWASP API Security](https://owasp.org/www-project-api-security/)
- [bcrypt for Go](https://pkg.go.dev/golang.org/x/crypto/bcrypt)
