package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Runner struct {
	Workdir          string
	useLegacyCompose bool
}

// detectLegacyCompose checks if "docker compose" works, otherwise uses "docker-compose" binary
func detectLegacyCompose() bool {
	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		// docker compose subcommand not available, check for docker-compose binary
		if _, err := exec.LookPath("docker-compose"); err == nil {
			log.Printf("docker: using legacy docker-compose binary")
			return true
		}
	}
	return false
}

var useLegacyCompose = detectLegacyCompose()

// ComposeCommand returns the compose command being used (for diagnostics)
func ComposeCommand() string {
	if useLegacyCompose {
		return "docker-compose"
	}
	return "docker compose"
}

func NewRunner(workdir string) *Runner {
	return &Runner{Workdir: workdir, useLegacyCompose: useLegacyCompose}
}

func (r *Runner) Up(ctx context.Context, extraArgs ...string) (string, error) {
	args := append([]string{"compose", "up", "-d"}, extraArgs...)
	return r.run(ctx, args...)
}

func (r *Runner) Pull(ctx context.Context, services ...string) (string, error) {
	args := append([]string{"compose", "pull"}, services...)
	return r.run(ctx, args...)
}

func (r *Runner) PS(ctx context.Context) (string, error) {
	return r.run(ctx, "compose", "ps")
}

type ContainerStatus struct {
	Name      string     `json:"Name"`
	Service   string     `json:"Service"`
	State     string     `json:"State"`
	Health    string     `json:"Health"`
	StartedAt *time.Time `json:"-"`
}

// PSStatus returns structured container status information if available along with the raw output.
func (r *Runner) PSStatus(ctx context.Context) ([]ContainerStatus, string, error) {
	out, err := r.run(ctx, "compose", "ps", "--format", "json")
	if err != nil {
		return nil, out, err
	}
	var containers []ContainerStatus
	if err := json.Unmarshal([]byte(out), &containers); err == nil {
		return containers, out, nil
	}
	// Fallback: docker compose can emit JSON objects per line.
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c ContainerStatus
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, out, fmt.Errorf("parse compose ps json: %w", err)
		}
		containers = append(containers, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, out, fmt.Errorf("read compose ps json: %w", err)
	}
	return containers, out, nil
}

// InspectStartedAt returns container start times keyed by container name.
func (r *Runner) InspectStartedAt(ctx context.Context, names []string) (map[string]time.Time, error) {
	started := make(map[string]time.Time)
	if len(names) == 0 {
		return started, nil
	}

	args := append([]string{"inspect", "--format", "{{.Name}}|{{.State.StartedAt}}"}, names...)
	out, err := r.run(ctx, args...)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimPrefix(parts[0], "/")
		if name == "" {
			continue
		}
		if parts[1] == "" {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, parts[1])
		if err != nil {
			continue
		}
		started[name] = when
	}
	if err := scanner.Err(); err != nil {
		return started, err
	}
	return started, nil
}

// WaitHealthy polls container status until all target services are running and healthy.
// If services is empty, it checks all services. Returns error on timeout or context cancellation.
func (r *Runner) WaitHealthy(ctx context.Context, services []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Build a set of target services for quick lookup
	targets := make(map[string]bool)
	for _, s := range services {
		targets[strings.ToLower(s)] = true
	}
	checkAll := len(targets) == 0

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for services to become healthy")
		}

		containers, _, err := r.PSStatus(ctx)
		if err == nil && len(containers) > 0 {
			allHealthy := true
			for _, c := range containers {
				svcName := strings.ToLower(c.Service)
				if !checkAll && !targets[svcName] {
					continue
				}

				// Check state is running (compose may append extra info)
				state := strings.ToLower(c.State)
				if !strings.HasPrefix(state, "running") {
					allHealthy = false
					break
				}

				// If container has health check, it must be healthy
				// Health field is empty string if no healthcheck defined
				health := strings.ToLower(c.Health)
				if health != "" && health != "healthy" {
					allHealthy = false
					break
				}
			}

			if allHealthy {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// continue polling
		}
	}
}

func (r *Runner) Logs(ctx context.Context, service string, tail int) (string, error) {
	args := []string{"compose", "logs"}
	if tail > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", tail))
	}
	args = append(args, service)
	return r.run(ctx, args...)
}

// LogsFollow streams logs to the provided writer until context is cancelled.
func (r *Runner) LogsFollow(ctx context.Context, service string, tail int, w io.Writer) error {
	composeArgs := []string{"logs", "-f", "--no-log-prefix"}
	if tail > 0 {
		composeArgs = append(composeArgs, "--tail", fmt.Sprintf("%d", tail))
	}
	composeArgs = append(composeArgs, service)

	var cmd *exec.Cmd
	if r.useLegacyCompose {
		cmd = exec.CommandContext(ctx, "docker-compose", composeArgs...)
	} else {
		cmd = exec.CommandContext(ctx, "docker", append([]string{"compose"}, composeArgs...)...)
	}
	cmd.Dir = r.Workdir
	cmd.Stdout = w
	cmd.Stderr = w

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start logs: %w", err)
	}

	return cmd.Wait()
}

func (r *Runner) run(ctx context.Context, args ...string) (string, error) {
	var cmd *exec.Cmd
	var cmdDesc string

	// Use docker-compose binary if docker compose subcommand isn't available
	if len(args) > 0 && args[0] == "compose" && r.useLegacyCompose {
		cmd = exec.CommandContext(ctx, "docker-compose", args[1:]...)
		cmdDesc = "docker-compose " + strings.Join(args[1:], " ")
	} else {
		cmd = exec.CommandContext(ctx, "docker", args...)
		cmdDesc = "docker " + strings.Join(args, " ")
	}

	cmd.Dir = r.Workdir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		output := strings.TrimSpace(out.String())
		if output != "" {
			return output, fmt.Errorf("%s: %w\n%s", cmdDesc, err, output)
		}
		return output, fmt.Errorf("%s: %w", cmdDesc, err)
	}
	return out.String(), nil
}

// InspectImagePort inspects a Docker image and returns the first exposed port.
// Returns 0 if no ports are exposed. The image must be available locally.
func InspectImagePort(ctx context.Context, image string) (int, error) {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image, "--format", "{{json .Config.ExposedPorts}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("inspect image %s: %w", image, err)
	}

	output := strings.TrimSpace(out.String())
	if output == "" || output == "null" || output == "{}" {
		return 0, nil
	}

	var ports map[string]interface{}
	if err := json.Unmarshal([]byte(output), &ports); err != nil {
		return 0, fmt.Errorf("parse exposed ports: %w", err)
	}

	for portSpec := range ports {
		parts := strings.Split(portSpec, "/")
		if len(parts) >= 1 {
			port, err := strconv.Atoi(parts[0])
			if err == nil && port > 0 {
				return port, nil
			}
		}
	}

	return 0, nil
}

// InspectImageVolumes inspects a Docker image and returns declared volume mountpoints.
// Returns empty slice if no volumes are declared. The image must be available locally.
func InspectImageVolumes(ctx context.Context, image string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image, "--format", "{{json .Config.Volumes}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("inspect image %s: %w", image, err)
	}

	output := strings.TrimSpace(out.String())
	if output == "" || output == "null" || output == "{}" {
		return nil, nil
	}

	var volumes map[string]any
	if err := json.Unmarshal([]byte(output), &volumes); err != nil {
		return nil, fmt.Errorf("parse volumes: %w", err)
	}

	paths := make([]string, 0, len(volumes))
	for path := range volumes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// PullImage pulls a Docker image.
func PullImage(ctx context.Context, image string) error {
	cmd := exec.CommandContext(ctx, "docker", "pull", image)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pull image %s: %w\n%s", image, err, out.String())
	}
	return nil
}
