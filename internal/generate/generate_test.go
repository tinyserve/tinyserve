package generate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tinyserve/internal/state"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"myapp", "myapp"},
		{"MyApp", "myapp"},
		{"my app", "my-app"},
		{"my_app", "my-app"},
		{"my.app", "my-app"},
		{"  myapp  ", "myapp"},
		{"my--app", "my--app"},
		{"---myapp---", "myapp"},
		{"my@app!", "my-app"},
		{"123app", "123app"},
		{"", ""},
		{"a", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestUnique(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "no duplicates",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with duplicates",
			input:    []string{"a", "b", "a", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "empty strings filtered",
			input:    []string{"a", "", "b", ""},
			expected: []string{"a", "b"},
		},
		{
			name:     "all empty",
			input:    []string{"", "", ""},
			expected: nil,
		},
		{
			name:     "empty input",
			input:    []string{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := unique(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("unique(%v) = %v, want %v", tt.input, result, tt.expected)
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("unique(%v) = %v, want %v", tt.input, result, tt.expected)
					break
				}
			}
		})
	}
}

func TestQuoteList(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{[]string{"a"}, `"a"`},
		{[]string{"a", "b"}, `"a", "b"`},
		{[]string{"curl", "-f", "http://localhost/"}, `"curl", "-f", "http://localhost/"`},
		{[]string{}, ""},
	}

	for _, tt := range tests {
		result := quoteList(tt.input)
		if result != tt.expected {
			t.Errorf("quoteList(%v) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestCollectHostnames(t *testing.T) {
	tests := []struct {
		name     string
		state    state.State
		expected []string
	}{
		{
			name:     "empty state with default domain",
			state:    state.NewState(),
			expected: []string{"whoami.example.com"},
		},
		{
			name: "with custom domain",
			state: func() state.State {
				s := state.NewState()
				s.Settings.DefaultDomain = "test.io"
				return s
			}(),
			expected: []string{"whoami.test.io"},
		},
		{
			name: "with services",
			state: func() state.State {
				s := state.NewState()
				s.Settings.DefaultDomain = "test.io"
				s.Services = []state.Service{
					{Name: "app1", Enabled: true, Hostnames: []string{"app1.test.io"}},
					{Name: "app2", Enabled: true}, // Should use default hostname
				}
				return s
			}(),
			expected: []string{"whoami.test.io", "app1.test.io", "app2.test.io"},
		},
		{
			name: "disabled services excluded",
			state: func() state.State {
				s := state.NewState()
				s.Settings.DefaultDomain = "test.io"
				s.Services = []state.Service{
					{Name: "enabled", Enabled: true, Hostnames: []string{"enabled.test.io"}},
					{Name: "disabled", Enabled: false, Hostnames: []string{"disabled.test.io"}},
				}
				return s
			}(),
			expected: []string{"whoami.test.io", "enabled.test.io"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectHostnames(tt.state)
			if len(result) != len(tt.expected) {
				t.Errorf("collectHostnames() = %v, want %v", result, tt.expected)
				return
			}
			// Check all expected are present (order may vary)
			resultMap := make(map[string]bool)
			for _, h := range result {
				resultMap[h] = true
			}
			for _, exp := range tt.expected {
				if !resultMap[exp] {
					t.Errorf("collectHostnames() missing %q, got %v", exp, result)
				}
			}
		})
	}
}

func TestBuildTraefikLabels(t *testing.T) {
	svc := state.Service{
		Name:         "myapp",
		InternalPort: 8080,
		Hostnames:    []string{"myapp.example.com"},
		Enabled:      true,
	}

	labels := buildTraefikLabels("myapp", svc, "example.com")

	// Check essential labels exist
	expectedContains := []string{
		"traefik.enable=true",
		"traefik.http.routers.myapp-0.rule=Host(`myapp.example.com`)",
		"traefik.http.services.myapp.loadbalancer.server.port=8080",
	}

	for _, exp := range expectedContains {
		found := false
		for _, l := range labels {
			if l == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("buildTraefikLabels() missing label %q, got %v", exp, labels)
		}
	}
}

func TestBuildTraefikLabelsDisabled(t *testing.T) {
	svc := state.Service{
		Name:    "disabled-app",
		Enabled: false,
	}

	labels := buildTraefikLabels("disabled-app", svc, "example.com")

	// Should have traefik.enable=false
	found := false
	for _, l := range labels {
		if l == "traefik.enable=false" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildTraefikLabels() should have traefik.enable=false for disabled service")
	}
}

func TestBuildTraefikLabelsDefaultHostname(t *testing.T) {
	svc := state.Service{
		Name:         "nohost",
		InternalPort: 3000,
		Enabled:      true,
		// No Hostnames specified
	}

	labels := buildTraefikLabels("nohost", svc, "mydom.com")

	// Should generate hostname from name + domain
	found := false
	for _, l := range labels {
		if strings.Contains(l, "nohost.mydom.com") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildTraefikLabels() should generate default hostname, got %v", labels)
	}
}

func TestGenerateBaseFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-generate-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s := state.NewState()
	s.Settings.DefaultDomain = "test.example.com"
	s.Services = []state.Service{
		{
			ID:           "app-123",
			Name:         "testapp",
			Image:        "nginx:latest",
			InternalPort: 80,
			Enabled:      true,
			Hostnames:    []string{"testapp.test.example.com"},
		},
	}

	out, err := GenerateBaseFiles(context.Background(), s, tmpDir)
	if err != nil {
		t.Fatalf("GenerateBaseFiles() error = %v", err)
	}

	// Verify staging dir created
	if _, err := os.Stat(out.StagingDir); os.IsNotExist(err) {
		t.Error("staging directory not created")
	}

	// Verify docker-compose.yml exists and has content
	composeContent, err := os.ReadFile(out.ComposePath)
	if err != nil {
		t.Fatalf("failed to read compose file: %v", err)
	}
	if !strings.Contains(string(composeContent), "testapp") {
		t.Error("compose file missing testapp service")
	}
	if !strings.Contains(string(composeContent), "traefik") {
		t.Error("compose file missing traefik service")
	}
	if !strings.Contains(string(composeContent), "cloudflared") {
		t.Error("compose file missing cloudflared service")
	}

	// Verify cloudflared config exists
	cloudflaredContent, err := os.ReadFile(out.Cloudflared)
	if err != nil {
		t.Fatalf("failed to read cloudflared config: %v", err)
	}
	if !strings.Contains(string(cloudflaredContent), "testapp.test.example.com") {
		t.Error("cloudflared config missing hostname")
	}

	// Verify traefik dynamic config exists
	if _, err := os.Stat(out.Traefik); os.IsNotExist(err) {
		t.Error("traefik dynamic config not created")
	}

	// Verify hostnames returned
	if len(out.Hostnames) == 0 {
		t.Error("hostnames not collected")
	}
}

func TestGenerateComposeWithHealthcheck(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-generate-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s := state.NewState()
	s.Services = []state.Service{
		{
			Name:         "healthyapp",
			Image:        "myapp:latest",
			InternalPort: 8080,
			Enabled:      true,
			Healthcheck: &state.ServiceHealthcheck{
				Command:         []string{"curl", "-f", "http://localhost:8080/health"},
				IntervalSeconds: 30,
				TimeoutSeconds:  10,
				Retries:         3,
			},
		},
	}

	out, err := GenerateBaseFiles(context.Background(), s, tmpDir)
	if err != nil {
		t.Fatalf("GenerateBaseFiles() error = %v", err)
	}

	content, err := os.ReadFile(out.ComposePath)
	if err != nil {
		t.Fatalf("failed to read compose: %v", err)
	}

	// Verify healthcheck is in compose
	if !strings.Contains(string(content), "healthcheck:") {
		t.Error("compose missing healthcheck section")
	}
	if !strings.Contains(string(content), "interval: 30s") {
		t.Error("compose missing interval")
	}
	if !strings.Contains(string(content), "timeout: 10s") {
		t.Error("compose missing timeout")
	}
}

func TestGenerateComposeWithVolumes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-generate-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s := state.NewState()
	s.Services = []state.Service{
		{
			Name:         "volapp",
			Image:        "myapp:latest",
			InternalPort: 8080,
			Enabled:      true,
			Volumes:      []string{"/host/data:/app/data", "/host/config:/app/config:ro"},
		},
	}

	out, err := GenerateBaseFiles(context.Background(), s, tmpDir)
	if err != nil {
		t.Fatalf("GenerateBaseFiles() error = %v", err)
	}

	content, err := os.ReadFile(out.ComposePath)
	if err != nil {
		t.Fatalf("failed to read compose: %v", err)
	}

	if !strings.Contains(string(content), "volumes:") {
		t.Error("compose missing volumes section")
	}
	if !strings.Contains(string(content), "/host/data:/app/data") {
		t.Error("compose missing first volume")
	}
	if !strings.Contains(string(content), "/host/config:/app/config:ro") {
		t.Error("compose missing second volume")
	}
}

func TestGenerateComposeWithMemoryLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-generate-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s := state.NewState()
	s.Services = []state.Service{
		{
			Name:         "limitedapp",
			Image:        "myapp:latest",
			InternalPort: 8080,
			Enabled:      true,
			Resources:    state.ServiceResources{MemoryLimitMB: 512},
		},
	}

	out, err := GenerateBaseFiles(context.Background(), s, tmpDir)
	if err != nil {
		t.Fatalf("GenerateBaseFiles() error = %v", err)
	}

	content, err := os.ReadFile(out.ComposePath)
	if err != nil {
		t.Fatalf("failed to read compose: %v", err)
	}

	if !strings.Contains(string(content), "memory: 512m") {
		t.Error("compose missing memory limit")
	}
}

func TestGenerateComposeWithEnv(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-generate-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s := state.NewState()
	s.Services = []state.Service{
		{
			Name:         "envapp",
			Image:        "myapp:latest",
			InternalPort: 8080,
			Enabled:      true,
			Env: map[string]string{
				"DATABASE_URL": "postgres://localhost/db",
				"LOG_LEVEL":    "debug",
			},
		},
	}

	out, err := GenerateBaseFiles(context.Background(), s, tmpDir)
	if err != nil {
		t.Fatalf("GenerateBaseFiles() error = %v", err)
	}

	content, err := os.ReadFile(out.ComposePath)
	if err != nil {
		t.Fatalf("failed to read compose: %v", err)
	}

	if !strings.Contains(string(content), "environment:") {
		t.Error("compose missing environment section")
	}
	if !strings.Contains(string(content), "DATABASE_URL") {
		t.Error("compose missing DATABASE_URL")
	}
	if !strings.Contains(string(content), "LOG_LEVEL") {
		t.Error("compose missing LOG_LEVEL")
	}
}

func TestStagingDirUniqueness(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-generate-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s := state.NewState()

	// Generate twice
	out1, err := GenerateBaseFiles(context.Background(), s, tmpDir)
	if err != nil {
		t.Fatalf("first GenerateBaseFiles() error = %v", err)
	}

	out2, err := GenerateBaseFiles(context.Background(), s, tmpDir)
	if err != nil {
		t.Fatalf("second GenerateBaseFiles() error = %v", err)
	}

	// Staging dirs should be different
	if out1.StagingDir == out2.StagingDir {
		t.Error("staging directories should be unique")
	}

	// Both should start with .staging-
	if !strings.Contains(filepath.Base(out1.StagingDir), ".staging-") {
		t.Error("staging dir should have .staging- prefix")
	}
}
