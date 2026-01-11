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
	case "launchd":
		err = cmdLaunchd(os.Args[2:])
	case "remote":
		err = cmdRemote(os.Args[2:])
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
  init --cloudflare-api-token T --tunnel-name N [--default-domain D] [--account-id ID]
  service add --name --image --port [--hostname h] [--env K=V] [--env-file .env]
               [--mem MB] [--volume host:container] [--healthcheck "CMD ..."]
  service list                 list all services
  service remove --name NAME   remove a service
  deploy [--service NAME] [--timeout SEC]  pull, restart, and wait for health
  logs --service NAME [--tail N] [--follow]
  rollback                     restore last backup
  launchd install              install and load launchd agent
  launchd uninstall            unload and remove launchd agent
  launchd status               show launchd agent status

remote:
  remote enable --hostname H   enable remote access via Cloudflare Tunnel
  remote disable               disable remote access
  remote token create [--name] create a deploy token
  remote token list            list all tokens
  remote token revoke <id>     revoke a token
`)
}

func cmdInit(args []string) error {
	var domain, apiToken, tunnelName, accountID string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--default-domain":
			i++
			if i >= len(args) {
				return fmt.Errorf("--default-domain requires a value")
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

	if apiToken == "" || tunnelName == "" {
		return fmt.Errorf("--cloudflare-api-token and --tunnel-name are required")
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
		case "--env-file":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--env-file requires a path")
			}
			envFromFile, err := parseEnvFile(args[i])
			if err != nil {
				return opts, fmt.Errorf("parse env file: %w", err)
			}
			for k, v := range envFromFile {
				opts.Env[k] = v
			}
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

func parseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	env := make(map[string]string)
	for lineNum, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("line %d: invalid format, expected KEY=value", lineNum+1)
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		// Remove surrounding quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		env[key] = value
	}

	return env, nil
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

func cmdLaunchd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve launchd <install|uninstall|status>")
	}
	switch args[0] {
	case "install":
		return cmdLaunchdInstall()
	case "uninstall":
		return cmdLaunchdUninstall()
	case "status":
		return cmdLaunchdStatus()
	default:
		return fmt.Errorf("unknown launchd subcommand: %s", args[0])
	}
}

const launchdLabel = "dev.tinyserve.daemon"

func launchdPlistPath() string {
	return os.ExpandEnv("$HOME/Library/LaunchAgents/dev.tinyserve.daemon.plist")
}

func cmdLaunchdInstall() error {
	plistPath := launchdPlistPath()

	// Find the tinyserved binary path
	tinyservedPath, err := exec.LookPath("tinyserved")
	if err != nil {
		// Try common locations
		candidates := []string{
			"/opt/homebrew/bin/tinyserved",
			"/usr/local/bin/tinyserved",
			os.ExpandEnv("$HOME/go/bin/tinyserved"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				tinyservedPath = p
				break
			}
		}
		if tinyservedPath == "" {
			return fmt.Errorf("tinyserved not found in PATH or common locations")
		}
	}

	// Ensure LaunchAgents directory exists
	launchAgentsDir := os.ExpandEnv("$HOME/Library/LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	// Generate plist content
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>

  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
  </array>

  <key>EnvironmentVariables</key>
  <dict>
    <key>TINYSERVE_API</key>
    <string>http://127.0.0.1:7070</string>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>

  <key>ThrottleInterval</key>
  <integer>10</integer>

  <key>StandardOutPath</key>
  <string>/tmp/tinyserved.log</string>

  <key>StandardErrorPath</key>
  <string>/tmp/tinyserved.err</string>

  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
`, launchdLabel, tinyservedPath)

	// Check if already installed
	if _, err := os.Stat(plistPath); err == nil {
		// Unload first if exists
		exec.Command("launchctl", "unload", plistPath).Run()
	}

	// Write plist
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Load the agent
	cmd := exec.Command("launchctl", "load", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %w\n%s", err, output)
	}

	fmt.Printf("✓ Installed launchd agent\n")
	fmt.Printf("  Binary: %s\n", tinyservedPath)
	fmt.Printf("  Plist:  %s\n", plistPath)
	fmt.Printf("  Logs:   /tmp/tinyserved.log, /tmp/tinyserved.err\n")
	return nil
}

func cmdLaunchdUninstall() error {
	plistPath := launchdPlistPath()

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return fmt.Errorf("launchd agent not installed (no plist at %s)", plistPath)
	}

	// Unload the agent
	cmd := exec.Command("launchctl", "unload", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: launchctl unload: %s\n", output)
	}

	// Remove the plist
	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Println("✓ Uninstalled launchd agent")
	return nil
}

func cmdLaunchdStatus() error {
	plistPath := launchdPlistPath()

	// Check plist exists
	fmt.Print("Plist installed............... ")
	if _, err := os.Stat(plistPath); err != nil {
		fmt.Println("✗ NOT FOUND")
		fmt.Printf("  Expected: %s\n", plistPath)
		return nil
	}
	fmt.Println("✓")

	// Check if loaded
	fmt.Print("Agent loaded.................. ")
	cmd := exec.Command("launchctl", "list")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("? (launchctl error)")
		return nil
	}

	loaded := false
	var pid string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, launchdLabel) {
			loaded = true
			fields := strings.Fields(line)
			if len(fields) >= 1 && fields[0] != "-" {
				pid = fields[0]
			}
			break
		}
	}

	if !loaded {
		fmt.Println("✗ NOT LOADED")
		fmt.Println("  Run: launchctl load " + plistPath)
		return nil
	}
	fmt.Println("✓")

	// Check if running
	fmt.Print("Daemon running................ ")
	if pid != "" && pid != "0" {
		fmt.Printf("✓ (PID %s)\n", pid)
	} else {
		fmt.Println("✗ NOT RUNNING")
		fmt.Println("  Check logs: cat /tmp/tinyserved.err")
	}

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

func cmdRemote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve remote <enable|disable|token> ...")
	}
	switch args[0] {
	case "enable":
		return cmdRemoteEnable(args[1:])
	case "disable":
		return cmdRemoteDisable()
	case "token":
		return cmdRemoteToken(args[1:])
	default:
		return fmt.Errorf("unknown remote subcommand: %s", args[0])
	}
}

func cmdRemoteEnable(args []string) error {
	var hostname string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--hostname":
			i++
			if i >= len(args) {
				return fmt.Errorf("--hostname requires a value")
			}
			hostname = args[i]
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if hostname == "" {
		return fmt.Errorf("--hostname is required")
	}

	payload := map[string]any{
		"enabled":  true,
		"hostname": hostname,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/remote/enable", bytes.NewReader(body))
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
		return fmt.Errorf("enable remote failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	fmt.Printf("✓ Remote access enabled at %s\n", hostname)
	fmt.Println("  Note: DNS and tunnel routing must be configured separately")
	return nil
}

func cmdRemoteDisable() error {
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/remote/disable", nil)
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
		return fmt.Errorf("disable remote failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	fmt.Println("✓ Remote access disabled")
	return nil
}

func cmdRemoteToken(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve remote token <create|list|revoke> ...")
	}
	switch args[0] {
	case "create":
		return cmdRemoteTokenCreate(args[1:])
	case "list":
		return cmdRemoteTokenList()
	case "revoke":
		return cmdRemoteTokenRevoke(args[1:])
	default:
		return fmt.Errorf("unknown token subcommand: %s", args[0])
	}
}

func cmdRemoteTokenCreate(args []string) error {
	var name string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			name = args[i]
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	payload := map[string]any{}
	if name != "" {
		payload["name"] = name
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/tokens", bytes.NewReader(body))
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
		return fmt.Errorf("create token failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	fmt.Printf("Token created: %s\n", result["token"])
	fmt.Printf("ID: %s\n", result["id"])
	if n, ok := result["name"].(string); ok && n != "" {
		fmt.Printf("Name: %s\n", n)
	}
	fmt.Println("\n⚠️  Store this token securely - it won't be shown again")
	return nil
}

func cmdRemoteTokenList() error {
	resp, err := http.Get(apiBase() + "/tokens")
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("list tokens failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}

	var tokens []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return err
	}

	if len(tokens) == 0 {
		fmt.Println("No tokens configured")
		return nil
	}

	fmt.Printf("%-18s %-20s %-24s %-24s\n", "ID", "NAME", "CREATED", "LAST USED")
	fmt.Println(strings.Repeat("-", 90))
	for _, t := range tokens {
		id, _ := t["id"].(string)
		name, _ := t["name"].(string)
		createdAt, _ := t["created_at"].(string)
		lastUsed, _ := t["last_used"].(string)
		if lastUsed == "" {
			lastUsed = "never"
		}
		fmt.Printf("%-18s %-20s %-24s %-24s\n", id, name, formatTime(createdAt), formatTime(lastUsed))
	}
	return nil
}

func formatTime(ts string) string {
	if ts == "" || ts == "never" {
		return ts
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func cmdRemoteTokenRevoke(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve remote token revoke <token-id>")
	}
	tokenID := args[0]

	req, err := http.NewRequest(http.MethodDelete, apiBase()+"/tokens/"+url.PathEscape(tokenID), nil)
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
		return fmt.Errorf("revoke token failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	fmt.Printf("✓ Token %s revoked\n", tokenID)
	return nil
}
