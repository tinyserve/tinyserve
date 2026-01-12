package validate

import (
	"fmt"
	"regexp"
	"strings"
)

// Docker image name validation
// Format: [registry/][namespace/]name[:tag][@digest]
// Examples: nginx, nginx:latest, ghcr.io/user/repo:v1.0
var imageNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*(?::[a-zA-Z0-9._-]+)?(?:@sha256:[a-fA-F0-9]{64})?$`)

// Environment variable key validation
// Must start with letter or underscore, contain only alphanumeric and underscore
var envKeyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Hostname validation (DNS format)
var hostnameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// Service name validation
var serviceNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// Volume path validation - basic format check
var volumePathRegex = regexp.MustCompile(`^[^:]+:[^:]+(?::(ro|rw))?$`)

// Dangerous host paths that should not be mounted
var dangerousHostPaths = []string{
	"/etc/passwd",
	"/etc/shadow",
	"/etc/sudoers",
	"/root/.ssh",
	"/var/run/docker.sock",
}

// ImageName validates a Docker image name
func ImageName(image string) error {
	if image == "" {
		return fmt.Errorf("image name is required")
	}
	if len(image) > 256 {
		return fmt.Errorf("image name too long (max 256 characters)")
	}
	if !imageNameRegex.MatchString(image) {
		return fmt.Errorf("invalid image name format: %q", image)
	}
	// Check for YAML injection attempts
	if containsYAMLInjection(image) {
		return fmt.Errorf("image name contains invalid characters")
	}
	return nil
}

// EnvKey validates an environment variable key
func EnvKey(key string) error {
	if key == "" {
		return fmt.Errorf("environment variable key is required")
	}
	if len(key) > 256 {
		return fmt.Errorf("environment variable key too long (max 256 characters)")
	}
	if !envKeyRegex.MatchString(key) {
		return fmt.Errorf("invalid environment variable key: %q (must start with letter or underscore, contain only alphanumeric and underscore)", key)
	}
	return nil
}

// EnvValue validates an environment variable value
func EnvValue(value string) error {
	if len(value) > 32768 {
		return fmt.Errorf("environment variable value too long (max 32KB)")
	}
	// Values are quoted in YAML, but check for null bytes
	if strings.Contains(value, "\x00") {
		return fmt.Errorf("environment variable value contains null byte")
	}
	return nil
}

// Hostname validates a DNS hostname
func Hostname(hostname string) error {
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	if len(hostname) > 253 {
		return fmt.Errorf("hostname too long (max 253 characters)")
	}
	if !hostnameRegex.MatchString(hostname) {
		return fmt.Errorf("invalid hostname format: %q", hostname)
	}
	return nil
}

// ServiceName validates a service name
func ServiceName(name string) error {
	if name == "" {
		return fmt.Errorf("service name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("service name too long (max 64 characters)")
	}
	if !serviceNameRegex.MatchString(name) {
		return fmt.Errorf("invalid service name: %q (must start with letter, contain only alphanumeric, underscore, and hyphen)", name)
	}
	return nil
}

// VolumePath validates a Docker volume mount specification
func VolumePath(volume string) error {
	if volume == "" {
		return fmt.Errorf("volume path is required")
	}
	if len(volume) > 4096 {
		return fmt.Errorf("volume path too long (max 4096 characters)")
	}
	if !volumePathRegex.MatchString(volume) {
		return fmt.Errorf("invalid volume format: %q (expected host:container or host:container:mode)", volume)
	}

	// Check for YAML injection
	if containsYAMLInjection(volume) {
		return fmt.Errorf("volume path contains invalid characters")
	}

	// Extract host path (first part before colon)
	parts := strings.SplitN(volume, ":", 2)
	hostPath := parts[0]

	// Check for dangerous paths
	for _, dangerous := range dangerousHostPaths {
		if strings.HasPrefix(hostPath, dangerous) || hostPath == dangerous {
			return fmt.Errorf("mounting %q is not allowed for security reasons", dangerous)
		}
	}

	// Disallow absolute paths starting with /etc, /var/run, /root
	if strings.HasPrefix(hostPath, "/etc/") ||
		strings.HasPrefix(hostPath, "/var/run/") ||
		strings.HasPrefix(hostPath, "/root/") ||
		hostPath == "/etc" ||
		hostPath == "/var/run" ||
		hostPath == "/root" {
		return fmt.Errorf("mounting system paths like %q is not allowed", hostPath)
	}

	return nil
}

// HealthcheckCommand validates a healthcheck command
func HealthcheckCommand(cmd []string) error {
	if len(cmd) == 0 {
		return nil // empty is allowed
	}
	if len(cmd) > 100 {
		return fmt.Errorf("healthcheck command has too many arguments (max 100)")
	}
	for i, arg := range cmd {
		if len(arg) > 4096 {
			return fmt.Errorf("healthcheck argument %d too long (max 4096 characters)", i)
		}
		if strings.Contains(arg, "\x00") {
			return fmt.Errorf("healthcheck argument contains null byte")
		}
	}
	return nil
}

// Port validates a port number
func Port(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return nil
}

// containsYAMLInjection checks for characters that could be used for YAML injection
func containsYAMLInjection(s string) bool {
	// Check for newlines (could inject new YAML keys)
	if strings.Contains(s, "\n") || strings.Contains(s, "\r") {
		return true
	}
	// Check for YAML special sequences at start
	if strings.HasPrefix(strings.TrimSpace(s), "---") ||
		strings.HasPrefix(strings.TrimSpace(s), "...") {
		return true
	}
	// Check for null bytes
	if strings.Contains(s, "\x00") {
		return true
	}
	return false
}
