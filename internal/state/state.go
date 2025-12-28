package state

import (
	"context"
	"errors"
	"sync"
	"time"
)

type TunnelMode string

const (
	TunnelModeToken           TunnelMode = "token"
	TunnelModeCredentialsFile TunnelMode = "credentials_file"
)

type TunnelSettings struct {
	Mode            TunnelMode `json:"mode"`
	Token           string     `json:"token,omitempty"`
	CredentialsFile string     `json:"credentials_file,omitempty"`
}

type GlobalSettings struct {
	ComposeProjectName string         `json:"compose_project_name"`
	DefaultDomain      string         `json:"default_domain,omitempty"`
	Tunnel             TunnelSettings `json:"tunnel"`
	UILocalPort        int            `json:"ui_local_port"`
}

type ServiceResources struct {
	MemoryLimitMB int `json:"memory_limit_mb"`
}

type ServiceHealthcheck struct {
	Command            []string `json:"command,omitempty"`
	IntervalSeconds    int      `json:"interval_seconds,omitempty"`
	TimeoutSeconds     int      `json:"timeout_seconds,omitempty"`
	Retries            int      `json:"retries,omitempty"`
	StartPeriodSeconds int      `json:"start_period_seconds,omitempty"`
}

type Service struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Image        string              `json:"image"`
	InternalPort int                 `json:"internal_port"`
	Hostnames    []string            `json:"hostnames,omitempty"`
	Env          map[string]string   `json:"env,omitempty"`
	Volumes      []string            `json:"volumes,omitempty"`
	Healthcheck  *ServiceHealthcheck `json:"healthcheck,omitempty"`
	Resources    ServiceResources    `json:"resources"`
	Enabled      bool                `json:"enabled"`
	LastDeploy   *time.Time          `json:"last_deploy,omitempty"`
	Status       string              `json:"status,omitempty"`
}

const ServiceTypeRegistryImage = "registry-image"

type State struct {
	Settings  GlobalSettings `json:"settings"`
	Services  []Service      `json:"services"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func NewState() State {
	now := time.Now().UTC()
	return State{
		Settings: GlobalSettings{
			ComposeProjectName: "tinyserve",
			Tunnel: TunnelSettings{
				Mode: TunnelModeToken,
			},
			UILocalPort: 7070,
		},
		Services:  []Service{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (s *State) Touch() {
	s.UpdatedAt = time.Now().UTC()
}

func (s State) Validate() error {
	if s.Settings.ComposeProjectName == "" {
		return errors.New("compose project name is required")
	}
	return nil
}

type Store interface {
	Load(ctx context.Context) (State, error)
	Save(ctx context.Context, s State) error
}

type InMemoryStore struct {
	mu    sync.RWMutex
	state State
}

func NewInMemoryStore(s State) *InMemoryStore {
	return &InMemoryStore{state: s}
}

func (m *InMemoryStore) Load(ctx context.Context) (State, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state, nil
}

func (m *InMemoryStore) Save(ctx context.Context, s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.Touch()
	m.state = s
	return nil
}
