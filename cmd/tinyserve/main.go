package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
  init                           interactive setup wizard
       [--cloudflare-api-token T] [--default-domain D] [--tunnel-name N] [--account-id ID] [--skip-cloudflare]
  service add --image [--name N] [--port P] [--hostname h] [--env K=V] [--env-file .env]
               [--mem MB] [--volume host:container] [--healthcheck "CMD ..."]
               [--auto-volumes | --no-auto-volumes]
               [--cloudflare] [--deploy] [--timeout SEC]
  service list                 list all services
  service edit --name NAME [--deploy] [--timeout SEC]
                               open service config in $EDITOR
  service remove --name NAME   remove a service
  deploy [--service NAME]... [--timeout SEC]  pull, restart, and wait for health
  logs --service NAME [--tail N] [--follow]
  rollback                     restore last backup
  launchd install              install and load launchd agent
  launchd uninstall            unload and remove launchd agent
  launchd status               show launchd agent status

remote:
  remote enable [--hostname H | --ui-hostname H] [--api-hostname H] [--cloudflare] [--deploy] [--timeout SEC]
                               enable remote access (--cloudflare to setup DNS/tunnel)
  remote disable               disable remote access
  remote token create [--name] [--service S]...
                               create a deploy token (--service restricts to specific services)
  remote token list            list all tokens
  remote token revoke <id>     revoke a token
  remote auth cloudflare-access --team-domain <domain> --policy-aud <aud>
                               enable Cloudflare Access authentication for UI
  remote auth disable          disable browser authentication (WARNING: UI will be public)
`)
}

func cmdInit(args []string) error {
	var domain, apiToken, tunnelName, accountID string
	var skipCloudflare bool

	// Parse flags
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
		case "--skip-cloudflare":
			skipCloudflare = true
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	reader := bufio.NewReader(os.Stdin)

	// Interactive mode if no flags provided
	if apiToken == "" && !skipCloudflare {
		fmt.Println("Welcome to TinyServe setup!")
		fmt.Println()
		fmt.Println("TinyServe can expose your services to the internet using Cloudflare Tunnel.")
		fmt.Println("This requires a free Cloudflare account with a domain.")
		fmt.Println()
		fmt.Print("Enable Cloudflare integration? [Y/n]: ")

		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))

		if answer == "n" || answer == "no" {
			skipCloudflare = true
		}
	}

	if skipCloudflare {
		fmt.Println()
		fmt.Println("Skipping Cloudflare integration.")
		fmt.Println("You can enable it later with: tinyserve init --cloudflare-api-token TOKEN")
		fmt.Println()
		fmt.Println("TinyServe is ready for local use!")
		return nil
	}

	// Get API token interactively if not provided
	if apiToken == "" {
		// Check if there's an existing token from a previous init attempt
		existingToken := getExistingCloudflareToken()
		if existingToken != "" {
			fmt.Println()
			fmt.Println("Found an existing Cloudflare API token from a previous init attempt.")
			fmt.Print("Use the existing token? [Y/n]: ")

			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))

			if answer != "n" && answer != "no" {
				apiToken = existingToken
			}
		}
	}

	if apiToken == "" {
		fmt.Println()
		fmt.Println("To create a Cloudflare API token:")
		fmt.Println("  1. Go to: https://dash.cloudflare.com/profile/api-tokens")
		fmt.Println("  2. Click 'Create Token'")
		fmt.Println("  3. Use 'Edit zone DNS' template or create custom with:")
		fmt.Println("     - Account > Account Settings > Read")
		fmt.Println("     - Account > Cloudflare Tunnel > Edit")
		fmt.Println("     - Zone > DNS > Edit")
		fmt.Println()
		fmt.Print("Open browser to create token? [Y/n]: ")

		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))

		if answer != "n" && answer != "no" {
			openBrowser("https://dash.cloudflare.com/profile/api-tokens")
		}

		fmt.Println()
		fmt.Print("Paste your Cloudflare API token: ")
		tokenInput, _ := reader.ReadString('\n')
		apiToken = strings.TrimSpace(tokenInput)

		if apiToken == "" {
			return fmt.Errorf("API token is required for Cloudflare integration")
		}
	}

	// Ask for default domain interactively
	if domain == "" {
		fmt.Println()
		fmt.Print("Default domain for services (optional, press Enter to skip): ")
		domainInput, _ := reader.ReadString('\n')
		domain = strings.TrimSpace(domainInput)
	}

	// Auto-generate tunnel name from hostname if not provided
	if tunnelName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "tinyserve"
		}
		tunnelName = "tinyserve-" + hostname
	}

	fmt.Println()
	fmt.Printf("Creating tunnel '%s'...\n", tunnelName)

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
		return fmt.Errorf("usage: tinyserve service <add|list|remove|edit> ...")
	}
	switch args[0] {
	case "add":
		return cmdServiceAdd(args[1:])
	case "list":
		return cmdServiceList()
	case "remove":
		return cmdServiceRemove(args[1:])
	case "edit":
		return cmdServiceEdit(args[1:])
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
		"auto_volumes":  opts.AutoVolumes,
		"resources": map[string]any{
			"memory_limit_mb": opts.Memory,
		},
		"cloudflare": opts.Cloudflare,
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
	if err := enc.Encode(out); err != nil {
		return err
	}

	// If --deploy flag is set, deploy the service and infrastructure
	if opts.Deploy {
		serviceName, _ := out["name"].(string)
		fmt.Println("Deploying service...")
		timeoutSec := opts.Timeout
		if timeoutSec == 0 {
			timeoutSec = 60
		}
		// Deploy infrastructure (traefik, cloudflared) and the new service
		services := []string{"traefik", "cloudflared", serviceName}
		if _, err := doDeploy(services, timeoutSec); err != nil {
			return fmt.Errorf("service added but deploy failed: %w", err)
		}
		fmt.Printf("✓ Service %s deployed\n", serviceName)
	}

	return nil
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

func cmdServiceEdit(args []string) error {
	var name string
	var deploy bool
	var timeoutSec int = 60
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			name = args[i]
		case "--deploy":
			deploy = true
		case "--timeout":
			i++
			if i >= len(args) {
				return fmt.Errorf("--timeout requires a value")
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
	if name == "" {
		return fmt.Errorf("--name is required")
	}

	// Fetch current service config
	resp, err := http.Get(apiBase() + "/services/" + url.PathEscape(name))
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("get service failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}

	var svc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&svc); err != nil {
		return fmt.Errorf("decode service: %w", err)
	}

	// Remove read-only fields from editor view
	delete(svc, "id")
	delete(svc, "last_deploy")
	delete(svc, "status")

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "tinyserve-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(svc); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Get editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	// Open editor
	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}

	// Read back edited config
	editedData, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("read edited file: %w", err)
	}

	// Validate JSON
	var edited map[string]any
	if err := json.Unmarshal(editedData, &edited); err != nil {
		return fmt.Errorf("invalid JSON after edit: %w", err)
	}

	// PUT updated service
	req, err := http.NewRequest(http.MethodPut, apiBase()+"/services/"+url.PathEscape(name), bytes.NewReader(editedData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update service failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}

	fmt.Printf("✓ Service %q updated\n", name)

	if deploy {
		fmt.Println("Deploying...")
		if _, err := doDeploy([]string{name}, timeoutSec); err != nil {
			return fmt.Errorf("deploy: %w", err)
		}
		fmt.Printf("✓ Service %q deployed\n", name)
	}

	return nil
}

type addOptions struct {
	Name        string
	Image       string
	Port        int
	Hostnames   []string
	Env         map[string]string
	Volumes     []string
	AutoVolumes bool
	Healthcheck string
	Memory      int
	Cloudflare  bool
	Deploy      bool
	Timeout     int
}

func parseServiceAdd(args []string) (addOptions, error) {
	opts := addOptions{
		Env:         map[string]string{},
		Memory:      256,
		AutoVolumes: true,
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
		case "--auto-volumes":
			opts.AutoVolumes = true
		case "--no-auto-volumes":
			opts.AutoVolumes = false
		case "--healthcheck":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--healthcheck requires a command")
			}
			opts.Healthcheck = args[i]
		case "--cloudflare":
			opts.Cloudflare = true
		case "--deploy":
			opts.Deploy = true
		case "--timeout":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--timeout requires a value in seconds")
			}
			t, err := strconv.Atoi(args[i])
			if err != nil {
				return opts, fmt.Errorf("invalid timeout: %w", err)
			}
			opts.Timeout = t
		default:
			return opts, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if opts.Image == "" {
		return opts, fmt.Errorf("--image is required")
	}
	if opts.Timeout > 0 && !opts.Deploy {
		return opts, fmt.Errorf("--timeout requires --deploy")
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
	var services []string
	timeoutSec := 60 // default 60 seconds
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--service":
			i++
			if i >= len(args) {
				return fmt.Errorf("--service requires a value")
			}
			services = append(services, args[i])
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
	out, err := doDeploy(services, timeoutSec)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func doDeploy(services []string, timeoutSec int) (map[string]any, error) {
	payload := map[string]any{
		"timeout_ms": timeoutSec * 1000,
	}
	if len(services) > 0 {
		payload["services"] = services
		if len(services) == 1 {
			payload["service"] = services[0]
		}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/deploy", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, wrapConnError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deploy failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
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

	// 3. Check Docker Compose available
	fmt.Print("Docker Compose available...... ")
	composeOK := false
	if _, err := exec.LookPath("docker"); err == nil {
		if err := exec.Command("docker", "compose", "version").Run(); err == nil {
			composeOK = true
			fmt.Println("✓")
		}
	}
	if !composeOK {
		if _, err := exec.LookPath("docker-compose"); err == nil {
			if err := exec.Command("docker-compose", "version").Run(); err == nil {
				composeOK = true
				fmt.Println("✓ (docker-compose)")
			}
		}
	}
	if !composeOK {
		fmt.Println("✗ NOT AVAILABLE")
		allPassed = false
	}

	// 4. Check tinyserved daemon responding
	fmt.Print("tinyserved daemon............. ")
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(apiBase() + "/status")
	var statusPayload map[string]any
	statusOK := false
	if err != nil {
		fmt.Println("✗ NOT RESPONDING")
		allPassed = false
	} else {
		defer resp.Body.Close()
		if resp.StatusCode < 300 {
			if err := json.NewDecoder(resp.Body).Decode(&statusPayload); err == nil {
				fmt.Println("✓")
				statusOK = true
			} else {
				fmt.Println("✗ INVALID JSON")
				allPassed = false
			}
		} else {
			fmt.Printf("✗ HTTP %d\n", resp.StatusCode)
			allPassed = false
		}
	}

	// 5. Check launchd agent installed (CLI or brew services)
	fmt.Print("launchd agent installed....... ")
	cliPlistPath := os.ExpandEnv("$HOME/Library/LaunchAgents/dev.tinyserve.daemon.plist")
	brewPlistPath := os.ExpandEnv("$HOME/Library/LaunchAgents/homebrew.mxcl.tinyserve.plist")
	cliInstalled := false
	brewInstalled := false
	if _, err := os.Stat(cliPlistPath); err == nil {
		cliInstalled = true
	}
	if _, err := os.Stat(brewPlistPath); err == nil {
		brewInstalled = true
	}
	if cliInstalled {
		fmt.Println("✓")
	} else if brewInstalled {
		fmt.Println("✓ (brew services)")
	} else {
		fmt.Println("✗ NOT INSTALLED")
		allPassed = false
	}

	// 6. Check FileVault disk encryption (macOS)
	fmt.Print("FileVault encryption.......... ")
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("fdesetup", "status")
		output, err := cmd.Output()
		if err != nil {
			fmt.Println("? (cannot check)")
		} else if strings.Contains(string(output), "FileVault is On") {
			fmt.Println("✓")
		} else {
			fmt.Println("⚠ OFF (secrets stored unencrypted)")
		}
	} else {
		fmt.Println("- (macOS only)")
	}

	// 7. Check launchd agent loaded (CLI or brew services)
	fmt.Print("launchd agent loaded.......... ")
	cmd = exec.Command("launchctl", "list")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("? (launchctl error)")
		allPassed = false
	} else if strings.Contains(string(output), "dev.tinyserve.daemon") {
		fmt.Println("✓")
	} else if strings.Contains(string(output), "homebrew.mxcl.tinyserve") {
		fmt.Println("✓ (brew services)")
	} else {
		fmt.Println("✗ NOT LOADED")
		allPassed = false
	}

	// 8. Check sleep disabled (macOS)
	fmt.Print("Sleep disabled (never)....... ")
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("pmset", "-g")
		output, err := cmd.Output()
		if err != nil {
			fmt.Println("? (pmset error)")
		} else {
			values := map[string]string{}
			scanner := bufio.NewScanner(bytes.NewReader(output))
			for scanner.Scan() {
				fields := strings.Fields(scanner.Text())
				if len(fields) < 2 {
					continue
				}
				switch fields[0] {
				case "sleep", "disksleep", "displaysleep", "powernap":
					values[fields[0]] = fields[1]
				}
			}
			required := []string{"sleep", "disksleep", "displaysleep", "powernap"}
			missing := []string{}
			nonZero := []string{}
			for _, key := range required {
				val, ok := values[key]
				if !ok {
					missing = append(missing, key)
					continue
				}
				if val != "0" {
					nonZero = append(nonZero, fmt.Sprintf("%s=%s", key, val))
				}
			}
			if len(missing) > 0 {
				fmt.Printf("? missing %s\n", strings.Join(missing, ", "))
			} else if len(nonZero) > 0 {
				fmt.Printf("⚠ %s\n", strings.Join(nonZero, ", "))
			} else {
				fmt.Println("✓")
			}
		}
	} else {
		fmt.Println("- (macOS only)")
	}

	// 9. Check auto-restart after updates disabled (macOS)
	fmt.Print("Auto-restart after updates... ")
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("defaults", "read", "/Library/Preferences/com.apple.SoftwareUpdate")
		output, err := cmd.Output()
		if err != nil {
			fmt.Println("? (defaults error)")
		} else {
			isTruthy := func(value string) bool {
				switch strings.ToLower(strings.TrimSpace(value)) {
				case "1", "true", "yes":
					return true
				default:
					return false
				}
			}
			settings := map[string]string{}
			scanner := bufio.NewScanner(bytes.NewReader(output))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || line == "{" || line == "}" {
					continue
				}
				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(strings.TrimSuffix(parts[1], ";"))
				settings[key] = value
			}

			if val, ok := settings["AutoUpdateRestartRequired"]; ok && isTruthy(val) {
				fmt.Println("⚠ enabled (restart required)")
			} else if val, ok := settings["AutomaticallyInstallMacOSUpdates"]; ok {
				if isTruthy(val) {
					fmt.Println("⚠ enabled")
				} else {
					fmt.Println("✓")
				}
			} else {
				fmt.Println("? (key missing)")
			}
		}
	} else {
		fmt.Println("- (macOS only)")
	}

	// 10. Check Xcode Command Line Tools (macOS)
	fmt.Print("Xcode CLT installed.......... ")
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("xcode-select", "-p")
		if err := cmd.Run(); err != nil {
			fmt.Println("✗ NOT INSTALLED")
			allPassed = false
		} else {
			fmt.Println("✓")
		}
	} else {
		fmt.Println("- (macOS only)")
	}

	// 11. Check Homebrew installed
	fmt.Print("Homebrew installed........... ")
	cmd = exec.Command("brew", "--version")
	if err := cmd.Run(); err != nil {
		fmt.Println("✗ NOT INSTALLED")
		allPassed = false
	} else {
		fmt.Println("✓")
	}

	// 12. Check data root present/writable
	fmt.Print("Data root writable............ ")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("? (home dir error)")
		allPassed = false
	} else {
		dataRoot := filepath.Join(homeDir, "Library", "Application Support", "tinyserve")
		info, err := os.Stat(dataRoot)
		if err != nil {
			fmt.Println("✗ NOT FOUND")
			allPassed = false
		} else if !info.IsDir() {
			fmt.Println("✗ NOT A DIRECTORY")
			allPassed = false
		} else {
			tmp, err := os.CreateTemp(dataRoot, ".checkwrite-*")
			if err != nil {
				fmt.Println("✗ NOT WRITABLE")
				allPassed = false
			} else {
				tmp.Close()
				_ = os.Remove(tmp.Name())
				fmt.Println("✓")
			}
		}
	}

	// 13. Check Cloudflare init status (if tinyserved is up)
	fmt.Print("Cloudflare init status........ ")
	if !statusOK {
		fmt.Println("- (tinyserved down)")
	} else {
		hasToken, _ := statusPayload["has_cloudflare_token"].(bool)
		_, hasTunnel := statusPayload["tunnel_config"].(map[string]any)
		switch {
		case hasToken && hasTunnel:
			fmt.Println("✓")
		case hasToken && !hasTunnel:
			fmt.Println("⚠ token set, tunnel missing")
		case !hasToken && hasTunnel:
			fmt.Println("⚠ tunnel set, token missing")
		default:
			fmt.Println("⚠ NOT CONFIGURED")
		}
	}

	fmt.Println(strings.Repeat("=", 40))
	if allPassed {
		fmt.Println("All checks passed!")
		return nil
	}
	fmt.Println("Some checks failed. See hints below:")
	fmt.Println("")
	fmt.Println("• Docker: Install from https://docker.com/products/docker-desktop")
	fmt.Println("• Launchd: Run 'brew services start tinyserve' or 'tinyserve launchd install'")
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
		return nil
	}

	// Version comparison
	fmt.Print("Version....................... ")
	runningVersion, installedVersion := getDaemonVersions()
	if runningVersion == "" {
		fmt.Println("? (cannot get running version)")
	} else if installedVersion == "" {
		fmt.Printf("%s (cannot get installed version)\n", runningVersion)
	} else if runningVersion == installedVersion {
		fmt.Printf("✓ %s\n", runningVersion)
	} else {
		fmt.Printf("✗ MISMATCH\n")
		fmt.Printf("  Running:   %s\n", runningVersion)
		fmt.Printf("  Installed: %s\n", installedVersion)
		fmt.Println("  Hint: run 'tinyserve launchd install' to restart with new version")
	}

	return nil
}

func getDaemonVersions() (running, installed string) {
	// Get running daemon version via API
	resp, err := http.Get(apiBase() + "/version")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var v struct {
				Version string `json:"version"`
			}
			if json.NewDecoder(resp.Body).Decode(&v) == nil {
				running = v.Version
			}
		}
	}

	// Get installed binary version
	tinyservedPath, err := exec.LookPath("tinyserved")
	if err != nil {
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
	}
	if tinyservedPath != "" {
		cmd := exec.Command(tinyservedPath, "--version")
		out, err := cmd.Output()
		if err == nil {
			// Output format: "tinyserve VERSION (COMMIT) built DATE OS/ARCH"
			// Extract just the version
			parts := strings.Fields(strings.TrimSpace(string(out)))
			if len(parts) >= 2 {
				installed = parts[1]
			}
		}
	}

	return running, installed
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

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}

func cmdRemote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve remote <enable|disable|token|auth> ...")
	}
	switch args[0] {
	case "enable":
		return cmdRemoteEnable(args[1:])
	case "disable":
		return cmdRemoteDisable()
	case "token":
		return cmdRemoteToken(args[1:])
	case "auth":
		return cmdRemoteAuth(args[1:])
	default:
		return fmt.Errorf("unknown remote subcommand: %s", args[0])
	}
}

func cmdRemoteEnable(args []string) error {
	var hostname string
	var uiHostname string
	var apiHostname string
	var cloudflare bool
	var deploy bool
	var timeoutSet bool
	timeoutSec := 60
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--hostname":
			i++
			if i >= len(args) {
				return fmt.Errorf("--hostname requires a value")
			}
			hostname = args[i]
		case "--ui-hostname":
			i++
			if i >= len(args) {
				return fmt.Errorf("--ui-hostname requires a value")
			}
			uiHostname = args[i]
		case "--api-hostname":
			i++
			if i >= len(args) {
				return fmt.Errorf("--api-hostname requires a value")
			}
			apiHostname = args[i]
		case "--cloudflare":
			cloudflare = true
		case "--deploy":
			deploy = true
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
			timeoutSet = true
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if uiHostname == "" {
		uiHostname = hostname
	}
	if uiHostname == "" && apiHostname == "" {
		return fmt.Errorf("--ui-hostname or --api-hostname is required")
	}
	if deploy && !cloudflare {
		return fmt.Errorf("--deploy requires --cloudflare")
	}
	if timeoutSet && !deploy {
		return fmt.Errorf("--timeout requires --deploy")
	}

	payload := map[string]any{
		"enabled":      true,
		"hostname":     uiHostname,
		"ui_hostname":  uiHostname,
		"api_hostname": apiHostname,
		"cloudflare":   cloudflare,
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
	if uiHostname != "" {
		fmt.Printf("✓ Remote UI enabled at %s\n", uiHostname)
	}
	if apiHostname != "" {
		fmt.Printf("✓ Remote API enabled at %s\n", apiHostname)
	}
	if cloudflare {
		fmt.Println("  Cloudflare DNS configured")
		if deploy {
			fmt.Println("  Starting tunnel (traefik + cloudflared)...")
			if _, err := doDeploy([]string{"traefik", "cloudflared"}, timeoutSec); err != nil {
				return fmt.Errorf("remote enabled but deploy failed: %w", err)
			}
			fmt.Println("  Tunnel started")
		} else {
			fmt.Println("  Run 'tinyserve deploy' to start the tunnel")
		}
	}

	// Show auth warning if UI is enabled but no auth configured
	if uiHostname != "" {
		if !hasAuthConfigured() {
			fmt.Println()
			fmt.Println("⚠️  WARNING: UI has no authentication configured!")
			fmt.Println("   Anyone with the URL can access your dashboard.")
			fmt.Println()
			fmt.Println("   To protect your UI with Cloudflare Access:")
			fmt.Println("   1. Create an Access Application in Cloudflare Zero Trust dashboard")
			fmt.Println("   2. Run: tinyserve remote auth cloudflare-access --team-domain <your-team>.cloudflareaccess.com --policy-aud <aud>")
		}
	}
	return nil
}

func hasAuthConfigured() bool {
	resp, err := http.Get(apiBase() + "/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return false
	}
	settings, ok := status["settings"].(map[string]any)
	if !ok {
		return false
	}
	remote, ok := settings["remote"].(map[string]any)
	if !ok {
		return false
	}
	browserAuth, ok := remote["browser_auth"].(map[string]any)
	if !ok {
		return false
	}
	authType, _ := browserAuth["type"].(string)
	return authType != "" && authType != "none"
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
	var services []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			name = args[i]
		case "--service":
			i++
			if i >= len(args) {
				return fmt.Errorf("--service requires a value")
			}
			services = append(services, args[i])
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	payload := map[string]any{}
	if name != "" {
		payload["name"] = name
	}
	if len(services) > 0 {
		payload["services"] = services
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
	if svcs, ok := result["services"].([]any); ok && len(svcs) > 0 {
		var names []string
		for _, s := range svcs {
			if name, ok := s.(string); ok {
				names = append(names, name)
			}
		}
		fmt.Printf("Services: %s\n", strings.Join(names, ", "))
	} else {
		fmt.Println("Services: all (unrestricted)")
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

	fmt.Printf("%-18s %-20s %-20s %-24s %-24s\n", "ID", "NAME", "SERVICES", "CREATED", "LAST USED")
	fmt.Println(strings.Repeat("-", 110))
	for _, t := range tokens {
		id, _ := t["id"].(string)
		name, _ := t["name"].(string)
		createdAt, _ := t["created_at"].(string)
		lastUsed, _ := t["last_used"].(string)
		if lastUsed == "" {
			lastUsed = "never"
		}
		services := "all"
		if svcs, ok := t["services"].([]any); ok && len(svcs) > 0 {
			var names []string
			for _, s := range svcs {
				if sn, ok := s.(string); ok {
					names = append(names, sn)
				}
			}
			services = strings.Join(names, ",")
			if len(services) > 18 {
				services = services[:15] + "..."
			}
		}
		fmt.Printf("%-18s %-20s %-20s %-24s %-24s\n", id, name, services, formatTime(createdAt), formatTime(lastUsed))
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

func getExistingCloudflareToken() string {
	resp, err := http.Get(apiBase() + "/init/token")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var result struct {
		CloudflareAPIToken string `json:"cloudflare_api_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.CloudflareAPIToken
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

func cmdRemoteAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve remote auth <cloudflare-access|disable>")
	}
	switch args[0] {
	case "cloudflare-access":
		return cmdRemoteAuthCloudflareAccess(args[1:])
	case "disable":
		return cmdRemoteAuthDisable()
	default:
		return fmt.Errorf("unknown auth type: %s (supported: cloudflare-access, disable)", args[0])
	}
}

func cmdRemoteAuthCloudflareAccess(args []string) error {
	var teamDomain, policyAUD string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--team-domain":
			i++
			if i >= len(args) {
				return fmt.Errorf("--team-domain requires a value")
			}
			teamDomain = args[i]
		case "--policy-aud":
			i++
			if i >= len(args) {
				return fmt.Errorf("--policy-aud requires a value")
			}
			policyAUD = args[i]
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if teamDomain == "" {
		return fmt.Errorf("--team-domain is required (e.g., yourteam.cloudflareaccess.com)")
	}
	if policyAUD == "" {
		return fmt.Errorf("--policy-aud is required (Application Audience tag from Cloudflare Access)")
	}

	payload := map[string]any{
		"type":        "cloudflare_access",
		"team_domain": teamDomain,
		"policy_aud":  policyAUD,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/remote/auth", bytes.NewReader(body))
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
		return fmt.Errorf("configure auth failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	fmt.Println("✓ Browser authentication configured: Cloudflare Access")
	fmt.Printf("  Team domain: %s\n", teamDomain)
	fmt.Println("  Restart not required - takes effect immediately")
	return nil
}

func cmdRemoteAuthDisable() error {
	payload := map[string]any{
		"type": "none",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/remote/auth", bytes.NewReader(body))
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
		return fmt.Errorf("disable auth failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))
	}
	fmt.Println("✓ Browser authentication disabled")
	fmt.Println("  WARNING: UI is now accessible without authentication!")
	return nil
}
