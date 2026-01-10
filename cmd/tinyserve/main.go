package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"tinyserve/internal/version"
)

const defaultAPIAddr = "http://127.0.0.1:7070"

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	var err error
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return
	case "status":
		err = cmdStatus()
	case "init":
		err = cmdInit(os.Args[2:])
	case "service":
		err = cmdService(os.Args[2:])
	case "deploy":
		err = cmdDeploy(os.Args[2:])
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "rollback":
		err = cmdRollback()
	case "checklist":
		err = cmdChecklist()
	default:
		usage()
		return
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdStatus() error {
	resp, err := http.Get(apiBase() + "/status")
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("status request failed: %s", resp.Status)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode status: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func apiBase() string {
	if fromEnv := os.Getenv("TINYSERVE_API"); fromEnv != "" {
		return fromEnv
	}
	return defaultAPIAddr
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: tinyserve <command>

commands:
  version                      show version info
  status                       show daemon status
  checklist                    check system requirements and status
  init --domain D --cloudflare-api-token T --tunnel-name N [--account-id ID]
  service add --name --image --port [--hostname h] [--env K=V] [--mem MB]
               [--volume host:container] [--healthcheck "CMD ..."]
  service list                 list all services
  service remove --name NAME   remove a service
  deploy [--service NAME] [--timeout SEC]  pull, restart, and wait for health
  logs --service NAME [--tail N] [--follow]
  rollback                     restore last backup
`)
}

func cmdInit(args []string) error {
	var domain, apiToken, tunnelName, accountID string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--domain":
			i++
			if i >= len(args) {
				return fmt.Errorf("--domain requires a value")
			}
			domain = args[i]
		case "--cloudflare-api-token":
			i++
			if i >= len(args) {
				return fmt.Errorf("--cloudflare-api-token requires a value")
			}
			apiToken = args[i]
		case "--tunnel-name":
			i++
			if i >= len(args) {
				return fmt.Errorf("--tunnel-name requires a value")
			}
			tunnelName = args[i]
		case "--account-id":
			i++
			if i >= len(args) {
				return fmt.Errorf("--account-id requires a value")
			}
			accountID = args[i]
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	if domain == "" || apiToken == "" || tunnelName == "" {
		return fmt.Errorf("--domain, --cloudflare-api-token, and --tunnel-name are required")
	}

	payload := map[string]any{
		"domain":      domain,
		"api_token":   apiToken,
		"tunnel_name": tunnelName,
	}
	if accountID != "" {
		payload["account_id"] = accountID
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/init", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("init failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func cmdService(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve service <add|list|remove> ...")
	}
	switch args[0] {
	case "add":
		return cmdServiceAdd(args[1:])
	case "list":
		return cmdServiceList()
	case "remove":
		return cmdServiceRemove(args[1:])
	default:
		return fmt.Errorf("unknown service subcommand: %s", args[0])
	}
}

func cmdServiceAdd(args []string) error {
	opts, err := parseServiceAdd(args)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"name":          opts.Name,
		"image":         opts.Image,
		"internal_port": opts.Port,
		"hostnames":     opts.Hostnames,
		"env":           opts.Env,
		"volumes":       opts.Volumes,
		"resources": map[string]any{
			"memory_limit_mb": opts.Memory,
		},
	}
	if opts.Healthcheck != "" {
		payload["healthcheck"] = map[string]any{
			"command": strings.Fields(opts.Healthcheck),
		}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/services", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add service failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func cmdServiceList() error {
	resp, err := http.Get(apiBase() + "/services")
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("list services failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	var services []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return err
	}

	if len(services) == 0 {
		fmt.Println("No services configured")
		return nil
	}

	// Print as table
	fmt.Printf("%-20s %-40s %-8s %-12s\n", "NAME", "IMAGE", "PORT", "STATUS")
	fmt.Println(strings.Repeat("-", 84))
	for _, svc := range services {
		name, _ := svc["name"].(string)
		image, _ := svc["image"].(string)
		port, _ := svc["internal_port"].(float64)
		status, _ := svc["status"].(string)
		if status == "" {
			status = "unknown"
		}
		// Truncate long image names
		if len(image) > 40 {
			image = image[:37] + "..."
		}
		fmt.Printf("%-20s %-40s %-8.0f %-12s\n", name, image, port, status)
	}
	return nil
}

func cmdServiceRemove(args []string) error {
	var name string
	for i := 0; i < len(args); i++ {
		if args[i] == "--name" {
			i++
			if i >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			name = args[i]
		} else {
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if name == "" {
		return fmt.Errorf("--name is required")
	}

	req, err := http.NewRequest(http.MethodDelete, apiBase()+"/services/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove service failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	fmt.Printf("Service %q removed\n", name)
	return nil
}

type addOptions struct {
	Name        string
	Image       string
	Port        int
	Hostnames   []string
	Env         map[string]string
	Volumes     []string
	Healthcheck string
	Memory      int
}

func parseServiceAdd(args []string) (addOptions, error) {
	opts := addOptions{
		Env:    map[string]string{},
		Memory: 256,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--name requires a value")
			}
			opts.Name = args[i]
		case "--image":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--image requires a value")
			}
			opts.Image = args[i]
		case "--hostname":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--hostname requires a value")
			}
			opts.Hostnames = append(opts.Hostnames, args[i])
		case "--port":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--port requires a value")
			}
			p, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("invalid port: %w", err)
			}
			opts.Port = p
		case "--env":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--env requires K=V")
			}
			kv := strings.SplitN(args[i], "=", 2)
			if len(kv) != 2 {
				return opts, fmt.Errorf("env must be K=V")
			}
			opts.Env[kv[0]] = kv[1]
		case "--mem":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--mem requires a value")
			}
			mem, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("invalid mem: %w", err)
			}
			opts.Memory = mem
		case "--volume":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--volume requires host:container path")
			}
			opts.Volumes = append(opts.Volumes, args[i])
		case "--healthcheck":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--healthcheck requires a command")
			}
			opts.Healthcheck = args[i]
		default:
			return opts, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if opts.Name == "" || opts.Image == "" || opts.Port == 0 {
		return opts, fmt.Errorf("--name, --image, and --port are required")
	}
	return opts, nil
}

func cmdDeploy(args []string) error {
	var service string
	timeoutSec := 60 // default 60 seconds
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--service":
			i++
			if i >= len(args) {
				return fmt.Errorf("--service requires a value")
			}
			service = args[i]
		case "--timeout":
			i++
			if i >= len(args) {
				return fmt.Errorf("--timeout requires a value in seconds")
			}
			t, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid timeout: %w", err)
			}
			timeoutSec = t
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	payload := map[string]any{
		"timeout_ms": timeoutSec * 1000,
	}
	if service != "" {
		payload["service"] = service
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/deploy", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deploy failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func cmdLogs(args []string) error {
	var service string
	var follow bool
	tail := 200
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--service":
			i++
			if i >= len(args) {
				return fmt.Errorf("--service requires a value")
			}
			service = args[i]
		case "--tail":
			i++
			if i >= len(args) {
				return fmt.Errorf("--tail requires a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid tail: %w", err)
			}
			tail = n
		case "--follow", "-f":
			follow = true
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if service == "" {
		return fmt.Errorf("--service is required")
	}
	q := url.Values{}
	q.Set("service", service)
	q.Set("tail", strconv.Itoa(tail))
	if follow {
		q.Set("follow", "1")
	}
	resp, err := http.Get(apiBase() + "/logs?" + q.Encode())
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("logs failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func cmdRollback() error {
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/rollback", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rollback failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func cmdChecklist() error {
	fmt.Println("tinyserve checklist")
	fmt.Println(strings.Repeat("=", 40))

	allPassed := true

	// 1. Check Docker installed
	fmt.Print("Docker installed.............. ")
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Println("✗ NOT FOUND")
		allPassed = false
	} else {
		fmt.Println("✓")
	}

	// 2. Check Docker daemon running
	fmt.Print("Docker daemon running......... ")
	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		fmt.Println("✗ NOT RUNNING")
		allPassed = false
	} else {
		fmt.Println("✓")
	}

	// 3. Check tinyserved daemon responding
	fmt.Print("tinyserved daemon............. ")
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(apiBase() + "/status")
	if err != nil {
		fmt.Println("✗ NOT RESPONDING")
		allPassed = false
	} else {
		resp.Body.Close()
		if resp.StatusCode < 300 {
			fmt.Println("✓")
		} else {
			fmt.Printf("✗ HTTP %d\n", resp.StatusCode)
			allPassed = false
		}
	}

	// 4. Check launchd agent installed
	fmt.Print("launchd agent installed....... ")
	plistPath := os.ExpandEnv("$HOME/Library/LaunchAgents/dev.tinyserve.daemon.plist")
	if _, err := os.Stat(plistPath); err != nil {
		fmt.Println("✗ NOT INSTALLED")
		allPassed = false
	} else {
		fmt.Println("✓")
	}

	// 5. Check launchd agent loaded
	fmt.Print("launchd agent loaded.......... ")
	cmd = exec.Command("launchctl", "list")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("? (launchctl error)")
		allPassed = false
	} else if strings.Contains(string(output), "dev.tinyserve.daemon") {
		fmt.Println("✓")
	} else {
		fmt.Println("✗ NOT LOADED")
		allPassed = false
	}

	fmt.Println(strings.Repeat("=", 40))
	if allPassed {
		fmt.Println("All checks passed!")
		return nil
	}
	fmt.Println("Some checks failed. See hints below:")
	fmt.Println("")
	fmt.Println("• Docker: Install from https://docker.com/products/docker-desktop")
	fmt.Println("• Daemon: Run 'tinyserved' or load the launchd agent")
	fmt.Println("• Launchd: cp docs/launchd/tinyserved.plist ~/Library/LaunchAgents/dev.tinyserve.daemon.plist")
	fmt.Println("           launchctl load ~/Library/LaunchAgents/dev.tinyserve.daemon.plist")
	return nil
}

func wrapConnError(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "dial tcp") {
		return fmt.Errorf("%w\n\nHint: Is the daemon running? Start it with:\n  tinyserved\n\nOr check status with:\n  launchctl list | grep tinyserve", err)
	}
	return err
}
