package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS settings (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	compose_project_name TEXT NOT NULL DEFAULT 'tinyserve',
	default_domain TEXT,
	tunnel_mode TEXT NOT NULL DEFAULT 'token',
	tunnel_token TEXT,
	tunnel_credentials_file TEXT,
	tunnel_id TEXT,
	tunnel_name TEXT,
	tunnel_account_id TEXT,
	ui_local_port INTEGER NOT NULL DEFAULT 7070,
	max_backups INTEGER DEFAULT 10,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS services (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT 'registry-image',
	image TEXT NOT NULL,
	internal_port INTEGER NOT NULL,
	hostnames TEXT,
	env TEXT,
	volumes TEXT,
	healthcheck TEXT,
	memory_limit_mb INTEGER DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 0,
	last_deploy TEXT,
	status TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_services_name ON services(name);
`

type SQLiteStore struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	store := &SQLiteStore{db: db, path: path}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var version int
	err := s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		if !isTableNotFoundError(err) {
			return fmt.Errorf("check version: %w", err)
		}
	}

	if version >= schemaVersion {
		return nil
	}

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	if _, err := s.db.Exec(`INSERT OR REPLACE INTO schema_version (version) VALUES (?)`, schemaVersion); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}

	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	st := NewState()

	var createdAt, updatedAt string
	var tunnelToken, tunnelCredFile, tunnelID, tunnelName, tunnelAccountID, defaultDomain sql.NullString
	var maxBackups sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT compose_project_name, default_domain, tunnel_mode, tunnel_token, 
		       tunnel_credentials_file, tunnel_id, tunnel_name, tunnel_account_id,
		       ui_local_port, max_backups, created_at, updated_at
		FROM settings WHERE id = 1
	`).Scan(
		&st.Settings.ComposeProjectName,
		&defaultDomain,
		&st.Settings.Tunnel.Mode,
		&tunnelToken,
		&tunnelCredFile,
		&tunnelID,
		&tunnelName,
		&tunnelAccountID,
		&st.Settings.UILocalPort,
		&maxBackups,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return st, nil
		}
		return State{}, fmt.Errorf("load settings: %w", err)
	}

	st.Settings.DefaultDomain = defaultDomain.String
	st.Settings.Tunnel.Token = tunnelToken.String
	st.Settings.Tunnel.CredentialsFile = tunnelCredFile.String
	st.Settings.Tunnel.TunnelID = tunnelID.String
	st.Settings.Tunnel.TunnelName = tunnelName.String
	st.Settings.Tunnel.AccountID = tunnelAccountID.String
	if maxBackups.Valid {
		st.Settings.MaxBackups = int(maxBackups.Int64)
	}

	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		st.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		st.UpdatedAt = t
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, image, internal_port, hostnames, env, volumes, 
		       healthcheck, memory_limit_mb, enabled, last_deploy, status
		FROM services
	`)
	if err != nil {
		return State{}, fmt.Errorf("load services: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var svc Service
		var hostnames, env, volumes, healthcheck, lastDeploy, status sql.NullString
		var enabled int

		if err := rows.Scan(
			&svc.ID, &svc.Name, &svc.Type, &svc.Image, &svc.InternalPort,
			&hostnames, &env, &volumes, &healthcheck,
			&svc.Resources.MemoryLimitMB, &enabled, &lastDeploy, &status,
		); err != nil {
			return State{}, fmt.Errorf("scan service: %w", err)
		}

		svc.Enabled = enabled == 1
		svc.Status = status.String

		if hostnames.Valid && hostnames.String != "" {
			_ = json.Unmarshal([]byte(hostnames.String), &svc.Hostnames)
		}
		if env.Valid && env.String != "" {
			_ = json.Unmarshal([]byte(env.String), &svc.Env)
		}
		if volumes.Valid && volumes.String != "" {
			_ = json.Unmarshal([]byte(volumes.String), &svc.Volumes)
		}
		if healthcheck.Valid && healthcheck.String != "" {
			var hc ServiceHealthcheck
			if err := json.Unmarshal([]byte(healthcheck.String), &hc); err == nil {
				svc.Healthcheck = &hc
			}
		}
		if lastDeploy.Valid && lastDeploy.String != "" {
			if t, err := time.Parse(time.RFC3339Nano, lastDeploy.String); err == nil {
				svc.LastDeploy = &t
			}
		}

		st.Services = append(st.Services, svc)
	}

	return st, rows.Err()
}

func (s *SQLiteStore) Save(ctx context.Context, st State) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := st.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st.Touch()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO settings (id, compose_project_name, default_domain, tunnel_mode, 
		                      tunnel_token, tunnel_credentials_file, tunnel_id, tunnel_name,
		                      tunnel_account_id, ui_local_port, max_backups, created_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			compose_project_name = excluded.compose_project_name,
			default_domain = excluded.default_domain,
			tunnel_mode = excluded.tunnel_mode,
			tunnel_token = excluded.tunnel_token,
			tunnel_credentials_file = excluded.tunnel_credentials_file,
			tunnel_id = excluded.tunnel_id,
			tunnel_name = excluded.tunnel_name,
			tunnel_account_id = excluded.tunnel_account_id,
			ui_local_port = excluded.ui_local_port,
			max_backups = excluded.max_backups,
			updated_at = excluded.updated_at
	`,
		st.Settings.ComposeProjectName,
		nullString(st.Settings.DefaultDomain),
		st.Settings.Tunnel.Mode,
		nullString(st.Settings.Tunnel.Token),
		nullString(st.Settings.Tunnel.CredentialsFile),
		nullString(st.Settings.Tunnel.TunnelID),
		nullString(st.Settings.Tunnel.TunnelName),
		nullString(st.Settings.Tunnel.AccountID),
		st.Settings.UILocalPort,
		st.Settings.MaxBackups,
		st.CreatedAt.Format(time.RFC3339Nano),
		st.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert settings: %w", err)
	}

	var existingIDs []string
	rows, err := tx.QueryContext(ctx, "SELECT id FROM services")
	if err != nil {
		return fmt.Errorf("query existing services: %w", err)
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		existingIDs = append(existingIDs, id)
	}
	rows.Close()

	newIDs := make(map[string]bool)
	for _, svc := range st.Services {
		newIDs[svc.ID] = true
	}
	for _, id := range existingIDs {
		if !newIDs[id] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM services WHERE id = ?", id); err != nil {
				return fmt.Errorf("delete service %s: %w", id, err)
			}
		}
	}

	for _, svc := range st.Services {
		hostnames, _ := json.Marshal(svc.Hostnames)
		env, _ := json.Marshal(svc.Env)
		volumes, _ := json.Marshal(svc.Volumes)
		var healthcheck []byte
		if svc.Healthcheck != nil {
			healthcheck, _ = json.Marshal(svc.Healthcheck)
		}
		var lastDeploy sql.NullString
		if svc.LastDeploy != nil {
			lastDeploy = sql.NullString{String: svc.LastDeploy.Format(time.RFC3339Nano), Valid: true}
		}
		enabled := 0
		if svc.Enabled {
			enabled = 1
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO services (id, name, type, image, internal_port, hostnames, env, volumes,
			                      healthcheck, memory_limit_mb, enabled, last_deploy, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				name = excluded.name,
				type = excluded.type,
				image = excluded.image,
				internal_port = excluded.internal_port,
				hostnames = excluded.hostnames,
				env = excluded.env,
				volumes = excluded.volumes,
				healthcheck = excluded.healthcheck,
				memory_limit_mb = excluded.memory_limit_mb,
				enabled = excluded.enabled,
				last_deploy = excluded.last_deploy,
				status = excluded.status
		`,
			svc.ID, svc.Name, svc.Type, svc.Image, svc.InternalPort,
			string(hostnames), string(env), string(volumes), string(healthcheck),
			svc.Resources.MemoryLimitMB, enabled, lastDeploy, nullString(svc.Status),
		)
		if err != nil {
			return fmt.Errorf("upsert service %s: %w", svc.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func isTableNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "no such table")
}
