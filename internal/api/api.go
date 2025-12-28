package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tinyserve/internal/docker"
	"tinyserve/internal/generate"
	"tinyserve/internal/state"
)

type Handler struct {
	Store         state.Store
	GeneratedRoot string
	BackupsDir    string
	StatePath     string
}

func NewHandler(store state.Store, generatedRoot, backupsDir, statePath string) *Handler {
	return &Handler{
		Store:         store,
		GeneratedRoot: generatedRoot,
		BackupsDir:    backupsDir,
		StatePath:     statePath,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/status", h.handleStatus)
	mux.HandleFunc("/services", h.handleServices)
	mux.HandleFunc("/deploy", h.handleDeploy)
	mux.HandleFunc("/rollback", h.handleRollback)
	mux.HandleFunc("/logs", h.handleLogs)
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
	}

	st.Services = append(st.Services, svc)
	if err := h.Store.Save(ctx, st); err != nil {
		http.Error(w, fmt.Sprintf("save state: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, svc)
}

type deployRequest struct {
	Service string `json:"service,omitempty"`
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
	if _, err := runner.Up(ctx, targets...); err != nil {
		http.Error(w, fmt.Sprintf("docker up: %v", err), http.StatusInternalServerError)
		return
	}

	ts := time.Now().UTC().Format("20060102-150405")
	if err := h.backupState(ts); err != nil {
		http.Error(w, fmt.Sprintf("backup state: %v", err), http.StatusInternalServerError)
		return
	}
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

	resp := map[string]any{
		"status": "deploy_started",
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
	runner := docker.NewRunner(h.currentDir())
	out, err := runner.Logs(r.Context(), sanitizeName(service), tail)
	if err != nil {
		http.Error(w, fmt.Sprintf("logs: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(out))
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
