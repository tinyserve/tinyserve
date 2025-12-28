package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Runner struct {
	Workdir string
}

func NewRunner(workdir string) *Runner {
	return &Runner{Workdir: workdir}
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
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// PSStatus returns structured container status information if available along with the raw output.
func (r *Runner) PSStatus(ctx context.Context) ([]ContainerStatus, string, error) {
	out, err := r.run(ctx, "compose", "ps", "--format", "json")
	if err != nil {
		return nil, out, err
	}
	var containers []ContainerStatus
	if err := json.Unmarshal([]byte(out), &containers); err != nil {
		return nil, out, fmt.Errorf("parse compose ps json: %w", err)
	}
	return containers, out, nil
}

func (r *Runner) Logs(ctx context.Context, service string, tail int) (string, error) {
	args := []string{"compose", "logs"}
	if tail > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", tail))
	}
	args = append(args, service)
	return r.run(ctx, args...)
}

func (r *Runner) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = r.Workdir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return out.String(), nil
}
