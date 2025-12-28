package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const defaultAPIAddr = "http://127.0.0.1:7070"

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	var err error
	switch os.Args[1] {
	case "status":
		err = cmdStatus()
	case "service":
		err = cmdService(os.Args[2:])
	case "deploy":
		err = cmdDeploy(os.Args[2:])
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "rollback":
		err = cmdRollback()
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
		return fmt.Errorf("request status: %w", err)
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
	fmt.Fprintf(os.Stderr, "usage: tinyserve <command>\n\ncommands:\n  status                       show daemon status\n  service add --name --image --port [--hostname h] [--env K=V] [--mem 256]\n  deploy [--service NAME]      pull and restart services\n  logs --service NAME [--tail N]\n  rollback                     restore last backup\n")
}

func cmdService(args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return fmt.Errorf("usage: tinyserve service add --name --image --port [--hostname h] [--env K=V] [--mem 256]")
	}
	opts, err := parseServiceAdd(args[1:])
	if err != nil {
		return err
	}
	payload := map[string]any{
		"name":          opts.Name,
		"image":         opts.Image,
		"internal_port": opts.Port,
		"hostnames":     opts.Hostnames,
		"env":           opts.Env,
		"resources": map[string]any{
			"memory_limit_mb": opts.Memory,
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/services", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
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

type addOptions struct {
	Name      string
	Image     string
	Port      int
	Hostnames []string
	Env       map[string]string
	Memory    int
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
	for i := 0; i < len(args); i++ {
		if args[i] == "--service" {
			i++
			if i >= len(args) {
				return fmt.Errorf("--service requires a value")
			}
			service = args[i]
		} else {
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	payload := map[string]any{}
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
		return err
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
	resp, err := http.Get(apiBase() + "/logs?" + q.Encode())
	if err != nil {
		return err
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
		return err
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
