package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tinyserve/internal/auth"
	"tinyserve/internal/cloudflare"
	"tinyserve/internal/docker"
	"tinyserve/internal/generate"
	"tinyserve/internal/state"
)

type Handler struct {
	Store          state.Store
	GeneratedRoot  string
	BackupsDir     string
	StatePath      string
	CloudflaredDir string
}

func NewHandler(store state.Store, generatedRoot, backupsDir, statePath, cloudflaredDir string) *Handler {
	return &Handler{
		Store:          store,
		GeneratedRoot:  generatedRoot,
		BackupsDir:     backupsDir,
		StatePath:      statePath,
		CloudflaredDir: cloudflaredDir,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	authMw := NewAuthMiddleware(h.Store)

	mux.HandleFunc("/status", h.handleStatus)
	mux.HandleFunc("/services", h.handleServicesWithAuth(authMw))
	mux.HandleFunc("/services/", authMw.RequireToken(h.handleServiceByName)) // DELETE /services/{name}
	mux.HandleFunc("/deploy", authMw.RequireToken(h.handleDeploy))
	mux.HandleFunc("/rollback", authMw.RequireToken(h.handleRollback))
	mux.HandleFunc("/logs", h.handleLogs)
	mux.HandleFunc("/init", authMw.RequireToken(h.handleInit))
	mux.HandleFunc("/health", h.handleHealth)

	mux.HandleFunc("/tokens", h.handleTokens)
	mux.HandleFunc("/tokens/", h.handleTokenByID)

	mux.HandleFunc("/remote/enable", h.handleRemoteEnable)
	mux.HandleFunc("/remote/disable", h.handleRemoteDisable)
}

func (h *Handler) handleServicesWithAuth(authMw *AuthMiddleware) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			authMw.RequireToken(h.handleServices)(w, r)
		} else {
			h.handleServices(w, r)
		}
	}
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.Store.Load(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	statusMap, _ := h.containerStatus(r.Context())
	proxy := summarizeContainer(statusMap["traefik"])
	tunnel := summarizeContainer(statusMap["cloudflared"])

	resp := map[string]any{
		"status":        "ok",
		"service_count": len(st.Services),
		"updated_at":    st.UpdatedAt.Format(time.RFC3339),
		"proxy":         proxy,
		"tunnel":        tunnel,
	}
	writeJSON(w, resp)
}

func (h *Handler) handleServices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st, err := h.Store.Load(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
			return
		}
		statusMap, _ := h.containerStatus(r.Context())
		services := make([]state.Service, 0, len(st.Services))
		for _, svc := range st.Services {
			c := svc
			if cs, ok := statusMap[sanitizeName(svc.Name)]; ok {
				c.Status = describeStatus(cs)
			} else if c.Status == "" {
				c.Status = "unknown"
			}
			services = append(services, c)
		}
		writeJSON(w, services)
	case http.MethodPost:
		h.handleAddService(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type addServiceRequest struct {
	ID           string                    `json:"id,omitempty"`
	Name         string                    `json:"name"`
	Type         string                    `json:"type,omitempty"`
	Image        string                    `json:"image"`
	InternalPort int                       `json:"internal_port"`
	Hostnames    []string                  `json:"hostnames,omitempty"`
	Env          map[string]string         `json:"env,omitempty"`
	Volumes      []string                  `json:"volumes,omitempty"`
	Healthcheck  *state.ServiceHealthcheck `json:"healthcheck,omitempty"`
	Resources    state.ServiceResources    `json:"resources"`
	Enabled      *bool                     `json:"enabled,omitempty"`
}

func (h *Handler) handleAddService(w http.ResponseWriter, r *http.Request) {
	var payload addServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	svc := state.Service{
		ID:           payload.ID,
		Name:         strings.TrimSpace(payload.Name),
		Type:         payload.Type,
		Image:        payload.Image,
		InternalPort: payload.InternalPort,
		Hostnames:    payload.Hostnames,
		Env:          payload.Env,
		Volumes:      payload.Volumes,
		Healthcheck:  payload.Healthcheck,
		Resources:    payload.Resources,
	}
	if payload.Enabled != nil {
		svc.Enabled = *payload.Enabled
	} else {
		svc.Enabled = true
	}

	if svc.Name == "" || svc.Image == "" || svc.InternalPort == 0 {
		http.Error(w, "name, image, and internal_port are required", http.StatusBadRequest)
		return
	}
	if svc.Type == "" {
		svc.Type = state.ServiceTypeRegistryImage
	}
	if svc.Resources.MemoryLimitMB == 0 {
		svc.Resources.MemoryLimitMB = 256
	}
	if svc.Enabled == false {
		svc.Enabled = true
	}
	if svc.ID == "" {
		svc.ID = fmt.Sprintf("%s-%d", sanitizeName(svc.Name), time.Now().Unix())
	}

	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}
	for _, existing := range st.Services {
		if strings.EqualFold(existing.Name, svc.Name) {
			http.Error(w, "service name already exists", http.StatusConflict)
			return
		}
		// Check for hostname collisions
		for _, existingHost := range existing.Hostnames {
			for _, newHost := range svc.Hostnames {
				if strings.EqualFold(existingHost, newHost) {
					http.Error(w, fmt.Sprintf("hostname %q already used by service %q", newHost, existing.Name), http.StatusConflict)
					return
				}
			}
		}
	}

	st.Services = append(st.Services, svc)
	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, svc)
}

func (h *Handler) handleServiceByName(w http.ResponseWriter, r *http.Request) {
	// Extract service name from path: /services/{name}
	name := strings.TrimPrefix(r.URL.Path, "/services/")
	if name == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		h.handleDeleteService(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleDeleteService(w http.ResponseWriter, r *http.Request, name string) {
	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	// Find and remove the service
	found := false
	var newServices []state.Service
	for _, svc := range st.Services {
		if strings.EqualFold(svc.Name, name) {
			found = true
			continue // skip this one
		}
		newServices = append(newServices, svc)
	}

	if !found {
		http.Error(w, fmt.Sprintf("service %q not found", name), http.StatusNotFound)
		return
	}

	st.Services = newServices
	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"status": "removed", "name": name})
}

type deployRequest struct {
	Service   string `json:"service,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"` // health check timeout in milliseconds, default 60000
}

func (h *Handler) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req deployRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Default timeout: 60 seconds
	timeout := 60 * time.Second
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}

	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	// Generate staging config from full state.
	out, err := generate.GenerateBaseFiles(ctx, st, h.GeneratedRoot)
	if err != nil {
		http.Error(w, fmt.Sprintf("generate: %v", err), http.StatusInternalServerError)
		return
	}

	runner := docker.NewRunner(out.StagingDir)
	targets := []string{}
	if req.Service != "" {
		targets = append(targets, sanitizeName(req.Service))
	}

	if _, err := runner.Pull(ctx, targets...); err != nil && !strings.Contains(err.Error(), "No such service") {
		http.Error(w, fmt.Sprintf("docker pull: %v", err), http.StatusInternalServerError)
		return
	}

	// Backup current state and config before applying changes
	ts := time.Now().UTC().Format("20060102-150405")
	if err := h.backupState(ts); err != nil {
		http.Error(w, fmt.Sprintf("backup state: %v", err), http.StatusInternalServerError)
		return
	}
	if err := h.backupCurrentConfig(ts); err != nil {
		http.Error(w, fmt.Sprintf("backup config: %v", err), http.StatusInternalServerError)
		return
	}

	// Start containers
	if _, err := runner.Up(ctx, targets...); err != nil {
		http.Error(w, fmt.Sprintf("docker up: %v", err), http.StatusInternalServerError)
		return
	}

	// Wait for services to become healthy
	if err := runner.WaitHealthy(ctx, targets, timeout); err != nil {
		// Health check failed - rollback to previous config
		rollbackErr := h.rollbackFromBackup(ctx, ts)
		if rollbackErr != nil {
			http.Error(w, fmt.Sprintf("health check failed: %v; rollback also failed: %v", err, rollbackErr), http.StatusInternalServerError)
			return
		}
		http.Error(w, fmt.Sprintf("health check failed, rolled back: %v", err), http.StatusInternalServerError)
		return
	}

	// Health check passed - promote staging to current
	if err := h.promote(out.StagingDir, ts); err != nil {
		http.Error(w, fmt.Sprintf("promote staging: %v", err), http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	for i := range st.Services {
		if req.Service == "" || strings.EqualFold(st.Services[i].Name, req.Service) {
			st.Services[i].LastDeploy = &now
		}
	}
	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}

	// Prune old backups
	maxBackups := st.Settings.MaxBackups
	if maxBackups == 0 {
		maxBackups = 10
	}
	_ = h.pruneBackups(maxBackups)

	resp := map[string]any{
		"status": "deployed",
		"time":   now.Format(time.RFC3339),
	}
	writeJSON(w, resp)
}

func (h *Handler) handleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	target, err := h.latestBackup()
	if err != nil {
		http.Error(w, fmt.Sprintf("rollback: %v", err), http.StatusBadRequest)
		return
	}

	current := h.currentDir()
	tmpBackup := filepath.Join(h.BackupsDir, "backup-"+time.Now().UTC().Format("20060102-150405")+"-current")
	_ = os.RemoveAll(tmpBackup)
	if _, err := os.Stat(current); err == nil {
		_ = os.Rename(current, tmpBackup)
	}
	if err := os.Rename(target, current); err != nil {
		http.Error(w, fmt.Sprintf("restore backup: %v", err), http.StatusInternalServerError)
		return
	}

	runner := docker.NewRunner(current)
	if _, err := runner.Up(ctx); err != nil {
		http.Error(w, fmt.Sprintf("docker up after rollback: %v", err), http.StatusInternalServerError)
		return
	}

	stateBackup := h.latestStateBackup()
	if stateBackup != "" && h.StatePath != "" {
		_ = copyFile(stateBackup, h.StatePath)
	}

	writeJSON(w, map[string]any{"status": "rolled_back", "from": filepath.Base(target)})
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	service := r.URL.Query().Get("service")
	if service == "" {
		http.Error(w, "service is required", http.StatusBadRequest)
		return
	}
	tail := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := strconv.Atoi(t); err == nil {
			tail = n
		}
	}

	follow := r.URL.Query().Get("follow") == "1"
	runner := docker.NewRunner(h.currentDir())

	if follow {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		ctx := r.Context()
		pr, pw := io.Pipe()
		go func() {
			_ = runner.LogsFollow(ctx, sanitizeName(service), tail, pw)
			pw.Close()
		}()

		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
		return
	}

	out, err := runner.Logs(r.Context(), sanitizeName(service), tail)
	if err != nil {
		http.Error(w, fmt.Sprintf("logs: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(out))
}

type initRequest struct {
	Domain     string `json:"domain"`
	APIToken   string `json:"api_token"`
	TunnelName string `json:"tunnel_name"`
	AccountID  string `json:"account_id,omitempty"`
}

func (h *Handler) handleInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req initRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.APIToken == "" || req.TunnelName == "" {
		http.Error(w, "api_token and tunnel_name are required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Verify Docker is available
	if err := h.checkDocker(ctx); err != nil {
		http.Error(w, fmt.Sprintf("docker check failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Create Cloudflare client
	cfClient := cloudflare.NewClient(req.APIToken)

	// Get account ID if not provided
	accountID := req.AccountID
	if accountID == "" {
		var err error
		accountID, err = cfClient.GetAccountID(ctx)
		if err != nil {
			http.Error(w, fmt.Sprintf("get account ID: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Check if tunnel already exists
	existing, err := cfClient.FindTunnel(ctx, accountID, req.TunnelName)
	if err != nil {
		http.Error(w, fmt.Sprintf("find tunnel: %v", err), http.StatusInternalServerError)
		return
	}

	var tunnelID string
	var creds *cloudflare.TunnelCredentials

	if existing != nil {
		// Tunnel exists, get its token
		tunnelID = existing.ID
	} else {
		// Create new tunnel
		tunnel, newCreds, err := cfClient.CreateTunnel(ctx, accountID, req.TunnelName)
		if err != nil {
			http.Error(w, fmt.Sprintf("create tunnel: %v", err), http.StatusInternalServerError)
			return
		}
		tunnelID = tunnel.ID
		creds = newCreds
	}

	// Get tunnel token for running cloudflared
	token, err := cfClient.GetTunnelToken(ctx, accountID, tunnelID)
	if err != nil {
		http.Error(w, fmt.Sprintf("get tunnel token: %v", err), http.StatusInternalServerError)
		return
	}

	// Write credentials file if we created a new tunnel
	credsPath := ""
	if creds != nil {
		credsPath = filepath.Join(h.CloudflaredDir, fmt.Sprintf("%s.json", tunnelID))
		credsData, _ := json.MarshalIndent(creds, "", "  ")
		if err := os.WriteFile(credsPath, credsData, 0o600); err != nil {
			http.Error(w, fmt.Sprintf("write credentials: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Update state
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	st.Settings.DefaultDomain = req.Domain
	st.Settings.Tunnel.Mode = state.TunnelModeToken
	st.Settings.Tunnel.Token = token
	st.Settings.Tunnel.TunnelID = tunnelID
	st.Settings.Tunnel.TunnelName = req.TunnelName
	st.Settings.Tunnel.AccountID = accountID
	if credsPath != "" {
		st.Settings.Tunnel.CredentialsFile = credsPath
	}

	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"status":      "initialized",
		"tunnel_id":   tunnelID,
		"tunnel_name": req.TunnelName,
		"domain":      req.Domain,
		"account_id":  accountID,
		"created":     existing == nil,
	}
	writeJSON(w, resp)
}

func (h *Handler) checkDocker(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker not available: %w", err)
	}
	return nil
}

func (h *Handler) promote(stagingDir, timestamp string) error {
	current := h.currentDir()
	backup := filepath.Join(h.BackupsDir, "backup-"+timestamp)

	if err := os.MkdirAll(h.BackupsDir, 0o755); err != nil {
		return fmt.Errorf("ensure backups dir: %w", err)
	}
	if _, err := os.Stat(current); err == nil {
		_ = os.RemoveAll(backup)
		if err := os.Rename(current, backup); err != nil {
			return fmt.Errorf("move current to backup: %w", err)
		}
	}
	if err := os.Rename(stagingDir, current); err != nil {
		return fmt.Errorf("promote staging: %w", err)
	}
	return nil
}

func (h *Handler) backupState(timestamp string) error {
	if h.StatePath == "" {
		return nil
	}
	if err := os.MkdirAll(h.BackupsDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(h.BackupsDir, "state-"+timestamp+".json")
	return copyFile(h.StatePath, dst)
}

// backupCurrentConfig copies current config directory to backups (if it exists).
func (h *Handler) backupCurrentConfig(timestamp string) error {
	current := h.currentDir()
	if _, err := os.Stat(current); os.IsNotExist(err) {
		// No current config to backup
		return nil
	}
	backup := filepath.Join(h.BackupsDir, "backup-"+timestamp)
	if err := os.MkdirAll(h.BackupsDir, 0o755); err != nil {
		return err
	}
	return copyDir(current, backup)
}

// rollbackFromBackup restores config from backup and restarts services.
func (h *Handler) rollbackFromBackup(ctx context.Context, timestamp string) error {
	backup := filepath.Join(h.BackupsDir, "backup-"+timestamp)
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		return fmt.Errorf("backup not found: %s", backup)
	}

	current := h.currentDir()
	_ = os.RemoveAll(current)
	if err := copyDir(backup, current); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}

	runner := docker.NewRunner(current)
	if _, err := runner.Up(ctx); err != nil {
		return fmt.Errorf("docker up after rollback: %w", err)
	}
	return nil
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		return copyFile(path, dstPath)
	})
}

// pruneBackups removes old backups keeping only the most recent maxKeep.
func (h *Handler) pruneBackups(maxKeep int) error {
	if maxKeep <= 0 {
		maxKeep = 10 // default
	}

	entries, err := os.ReadDir(h.BackupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Collect backup directories and state files
	var backupDirs, stateFiles []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "backup-") && e.IsDir() {
			backupDirs = append(backupDirs, name)
		} else if strings.HasPrefix(name, "state-") && strings.HasSuffix(name, ".json") {
			stateFiles = append(stateFiles, name)
		}
	}

	// Sort by name (which includes timestamp, so older first)
	sort.Strings(backupDirs)
	sort.Strings(stateFiles)

	// Remove oldest backup directories if over limit
	if len(backupDirs) > maxKeep {
		for _, name := range backupDirs[:len(backupDirs)-maxKeep] {
			_ = os.RemoveAll(filepath.Join(h.BackupsDir, name))
		}
	}

	// Remove oldest state files if over limit
	if len(stateFiles) > maxKeep {
		for _, name := range stateFiles[:len(stateFiles)-maxKeep] {
			_ = os.Remove(filepath.Join(h.BackupsDir, name))
		}
	}

	return nil
}

func (h *Handler) latestBackup() (string, error) {
	entries, err := os.ReadDir(h.BackupsDir)
	if err != nil {
		return "", fmt.Errorf("list backups: %w", err)
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "backup-") {
			backups = append(backups, filepath.Join(h.BackupsDir, e.Name()))
		}
	}
	if len(backups) == 0 {
		return "", fmt.Errorf("no backups found")
	}
	sort.Strings(backups)
	return backups[len(backups)-1], nil
}

func (h *Handler) latestStateBackup() string {
	entries, err := os.ReadDir(h.BackupsDir)
	if err != nil {
		return ""
	}
	var states []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "state-") && strings.HasSuffix(e.Name(), ".json") {
			states = append(states, filepath.Join(h.BackupsDir, e.Name()))
		}
	}
	if len(states) == 0 {
		return ""
	}
	sort.Strings(states)
	return states[len(states)-1]
}

func (h *Handler) currentDir() string {
	return filepath.Join(h.GeneratedRoot, "current")
}

func (h *Handler) containerStatus(ctx context.Context) (map[string]docker.ContainerStatus, error) {
	current := h.currentDir()
	if _, err := os.Stat(filepath.Join(current, "docker-compose.yml")); err != nil {
		return map[string]docker.ContainerStatus{}, nil
	}
	runner := docker.NewRunner(current)
	containers, _, err := runner.PSStatus(ctx)
	if err != nil {
		return nil, err
	}
	statusMap := make(map[string]docker.ContainerStatus)
	for _, c := range containers {
		statusMap[strings.ToLower(c.Service)] = c
	}
	return statusMap, nil
}

func summarizeContainer(c docker.ContainerStatus) map[string]string {
	if c.Service == "" {
		return nil
	}
	resp := map[string]string{
		"service": c.Service,
		"state":   c.State,
	}
	if c.Health != "" {
		resp["health"] = c.Health
	}
	return resp
}

func describeStatus(c docker.ContainerStatus) string {
	if c.Health != "" {
		if c.Health == "healthy" {
			return c.Health
		}
		return fmt.Sprintf("%s (%s)", c.State, c.Health)
	}
	return c.State
}

func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	return name
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(data)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	statusMap, err := h.containerStatus(ctx)

	result := map[string]any{
		"daemon": "ok",
	}

	if err != nil {
		result["error"] = err.Error()
	}

	proxyStatus := statusMap["traefik"]
	tunnelStatus := statusMap["cloudflared"]

	proxy := map[string]any{
		"running": proxyStatus.State == "running",
		"state":   proxyStatus.State,
	}
	if proxyStatus.Health != "" {
		proxy["health"] = proxyStatus.Health
	}
	if proxyStatus.State != "running" && proxyStatus.State != "" {
		proxy["error"] = fmt.Sprintf("proxy container not running: %s", proxyStatus.State)
	} else if proxyStatus.State == "" {
		proxy["error"] = "proxy container not found"
		proxy["running"] = false
	}

	tunnel := map[string]any{
		"running": tunnelStatus.State == "running",
		"state":   tunnelStatus.State,
	}
	if tunnelStatus.Health != "" {
		tunnel["health"] = tunnelStatus.Health
	}
	if tunnelStatus.State != "running" && tunnelStatus.State != "" {
		tunnel["error"] = fmt.Sprintf("tunnel container not running: %s", tunnelStatus.State)
	} else if tunnelStatus.State == "" {
		tunnel["error"] = "tunnel container not found"
		tunnel["running"] = false
	}

	result["proxy"] = proxy
	result["tunnel"] = tunnel

	allHealthy := proxyStatus.State == "running" && tunnelStatus.State == "running"
	if proxyStatus.Health != "" && proxyStatus.Health != "healthy" {
		allHealthy = false
	}
	if tunnelStatus.Health != "" && tunnelStatus.Health != "healthy" {
		allHealthy = false
	}
	result["healthy"] = allHealthy

	writeJSON(w, result)
}

type createTokenRequest struct {
	Name string `json:"name"`
}

type tokenResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
}

func (h *Handler) handleTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListTokens(w, r)
	case http.MethodPost:
		h.handleCreateToken(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleListTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	tokens := make([]tokenResponse, 0, len(st.Tokens))
	for _, t := range st.Tokens {
		tokens = append(tokens, tokenResponse{
			ID:        t.ID,
			Name:      t.Name,
			CreatedAt: t.CreatedAt,
			LastUsed:  t.LastUsed,
		})
	}
	writeJSON(w, tokens)
}

func (h *Handler) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = "unnamed"
	}

	plaintext, err := auth.GenerateToken()
	if err != nil {
		http.Error(w, fmt.Sprintf("generate token: %v", err), http.StatusInternalServerError)
		return
	}

	hash, err := auth.HashToken(plaintext)
	if err != nil {
		http.Error(w, fmt.Sprintf("hash token: %v", err), http.StatusInternalServerError)
		return
	}

	token := state.APIToken{
		ID:        auth.GenerateTokenID(),
		Name:      req.Name,
		Hash:      hash,
		CreatedAt: time.Now().UTC(),
	}

	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	st.Tokens = append(st.Tokens, token)
	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"id":         token.ID,
		"name":       token.Name,
		"token":      plaintext,
		"created_at": token.CreatedAt,
		"message":    "Store this token securely - it won't be shown again",
	})
}

func (h *Handler) handleTokenByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/tokens/")
	if id == "" {
		http.Error(w, "token ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		h.handleRevokeToken(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleRevokeToken(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	found := false
	var newTokens []state.APIToken
	for _, t := range st.Tokens {
		if t.ID == id {
			found = true
			continue
		}
		newTokens = append(newTokens, t)
	}

	if !found {
		http.Error(w, fmt.Sprintf("token %q not found", id), http.StatusNotFound)
		return
	}

	st.Tokens = newTokens
	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"status": "revoked", "id": id})
}

type remoteEnableRequest struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"`
}

func (h *Handler) handleRemoteEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req remoteEnableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Hostname == "" {
		http.Error(w, "hostname is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	st.Settings.Remote.Enabled = true
	st.Settings.Remote.Hostname = req.Hostname

	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"status":   "enabled",
		"hostname": req.Hostname,
	})
}

func (h *Handler) handleRemoteDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	st, err := h.Store.Load(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("load state: %v", err), http.StatusInternalServerError)
		return
	}

	st.Settings.Remote.Enabled = false
	st.Settings.Remote.Hostname = ""

	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"status": "disabled"})
}
