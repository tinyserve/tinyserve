package validate

import (
	"strings"
	"testing"
)

func TestImageName(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{"valid simple", "nginx", false},
		{"valid with tag", "nginx:latest", false},
		{"valid with version", "nginx:1.21.0", false},
		{"valid with registry", "ghcr.io/user/repo:v1.0", false},
		{"valid with digest", "nginx@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", false},
		{"valid dockerhub", "library/nginx:alpine", false},
		{"empty", "", true},
		{"yaml injection newline", "nginx\nprivileged: true", true},
		{"yaml injection carriage return", "nginx\rprivileged: true", true},
		{"too long", strings.Repeat("a", 257), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ImageName(tt.image)
			if (err != nil) != tt.wantErr {
				t.Errorf("ImageName(%q) error = %v, wantErr %v", tt.image, err, tt.wantErr)
			}
		})
	}
}

func TestEnvKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid simple", "FOO", false},
		{"valid with underscore", "FOO_BAR", false},
		{"valid lowercase", "foo_bar", false},
		{"valid start underscore", "_FOO", false},
		{"valid with numbers", "FOO123", false},
		{"empty", "", true},
		{"starts with number", "123FOO", true},
		{"contains hyphen", "FOO-BAR", true},
		{"contains space", "FOO BAR", true},
		{"contains dot", "FOO.BAR", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EnvKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("EnvKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
		})
	}
}

func TestEnvValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid simple", "foo", false},
		{"valid with spaces", "foo bar baz", false},
		{"valid with special chars", "foo=bar&baz", false},
		{"valid empty", "", false},
		{"contains null byte", "foo\x00bar", true},
		{"too long", strings.Repeat("a", 32769), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EnvValue(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("EnvValue(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		wantErr  bool
	}{
		{"valid simple", "example.com", false},
		{"valid subdomain", "api.example.com", false},
		{"valid with hyphen", "my-app.example.com", false},
		{"valid single label", "localhost", false},
		{"empty", "", true},
		{"starts with hyphen", "-example.com", true},
		{"ends with hyphen", "example-.com", true},
		{"contains underscore", "my_app.example.com", true},
		{"too long", strings.Repeat("a", 254), true},
		{"label too long", strings.Repeat("a", 64) + ".com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Hostname(tt.hostname)
			if (err != nil) != tt.wantErr {
				t.Errorf("Hostname(%q) error = %v, wantErr %v", tt.hostname, err, tt.wantErr)
			}
		})
	}
}

func TestServiceName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		wantErr     bool
	}{
		{"valid simple", "myapp", false},
		{"valid with hyphen", "my-app", false},
		{"valid with underscore", "my_app", false},
		{"valid with numbers", "app123", false},
		{"empty", "", true},
		{"starts with number", "123app", true},
		{"starts with hyphen", "-app", true},
		{"contains space", "my app", true},
		{"too long", strings.Repeat("a", 65), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ServiceName(tt.serviceName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ServiceName(%q) error = %v, wantErr %v", tt.serviceName, err, tt.wantErr)
			}
		})
	}
}

func TestVolumePath(t *testing.T) {
	tests := []struct {
		name    string
		volume  string
		wantErr bool
	}{
		{"valid simple", "/data:/app/data", false},
		{"valid with mode", "/data:/app/data:ro", false},
		{"valid relative", "./data:/app/data", false},
		{"empty", "", true},
		{"missing container path", "/data", true},
		{"yaml injection", "/data\nprivileged: true:/app", true},
		{"dangerous etc passwd", "/etc/passwd:/app/passwd", true},
		{"dangerous etc shadow", "/etc/shadow:/app/shadow", true},
		{"dangerous docker sock", "/var/run/docker.sock:/docker.sock", true},
		{"dangerous root ssh", "/root/.ssh:/app/ssh", true},
		{"dangerous etc dir", "/etc:/app/etc", true},
		{"dangerous var run", "/var/run:/app/run", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VolumePath(tt.volume)
			if (err != nil) != tt.wantErr {
				t.Errorf("VolumePath(%q) error = %v, wantErr %v", tt.volume, err, tt.wantErr)
			}
		})
	}
}

func TestPort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid 80", 80, false},
		{"valid 443", 443, false},
		{"valid 8080", 8080, false},
		{"valid max", 65535, false},
		{"valid min", 1, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"too high", 65536, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Port(tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("Port(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
			}
		})
	}
}

func TestHealthcheckCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     []string
		wantErr bool
	}{
		{"valid simple", []string{"curl", "-f", "http://localhost/"}, false},
		{"valid empty", []string{}, false},
		{"valid nil", nil, false},
		{"too many args", make([]string, 101), true},
		{"null byte in arg", []string{"curl", "http://localhost/\x00evil"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := HealthcheckCommand(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("HealthcheckCommand(%v) error = %v, wantErr %v", tt.cmd, err, tt.wantErr)
			}
		})
	}
}

func TestContainsYAMLInjection(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"clean string", "nginx:latest", false},
		{"newline injection", "nginx\nprivileged: true", true},
		{"carriage return", "nginx\rsecurity_opt:", true},
		{"yaml document start", "---nginx", true},
		{"yaml document end", "...nginx", true},
		{"null byte", "nginx\x00evil", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsYAMLInjection(tt.s)
			if got != tt.want {
				t.Errorf("containsYAMLInjection(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}
