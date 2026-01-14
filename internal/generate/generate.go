package generate

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"tinyserve/internal/state"
)

type Output struct {
	StagingDir   string
	ComposePath  string
	Cloudflared  string
	Traefik      string
	ServicesRoot string
	Hostnames    []string
}

// GenerateBaseFiles builds a staging directory with starter docker-compose and config files.
// It is intentionally minimal so the apply/rollback flow can consume it later.
func GenerateBaseFiles(ctx context.Context, s state.State, root string) (Output, error) {
	ts := time.Now().UTC().Format("20060102-150405")
	staging := filepath.Join(root, ".staging-"+ts)

	paths := []string{
		staging,
		filepath.Join(staging, "traefik"),
		filepath.Join(staging, "cloudflared"),
		filepath.Join(staging, "services"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return Output{}, fmt.Errorf("create staging dir %s: %w", p, err)
		}
	}

	composePath := filepath.Join(staging, "docker-compose.yml")
	cloudflaredPath := filepath.Join(staging, "cloudflared", "config.yml")
	traefikPath := filepath.Join(staging, "traefik", "dynamic.yml")

	if err := writeCompose(composePath, s); err != nil {
		return Output{}, err
	}
	hostnames := collectHostnames(s)
	if err := writeCloudflared(cloudflaredPath, s, hostnames); err != nil {
		return Output{}, err
	}
	if err := writeTraefikDynamic(traefikPath); err != nil {
		return Output{}, err
	}

	return Output{
		StagingDir:   staging,
		ComposePath:  composePath,
		Cloudflared:  cloudflaredPath,
		Traefik:      traefikPath,
		ServicesRoot: filepath.Join(staging, "services"),
		Hostnames:    hostnames,
	}, nil
}

func writeCompose(path string, s state.State) error {
	domain := s.Settings.DefaultDomain
	if domain == "" {
		domain = "example.com"
	}
	whoamiHost := "whoami." + domain

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("name: %s\n", s.Settings.ComposeProjectName))
	sb.WriteString("services:\n")
	sb.WriteString(`  traefik:
    image: traefik:v3.0
    command:
      - --providers.docker=true
      - --providers.docker.exposedbydefault=false
      - --entrypoints.web.address=:80
      - --accesslog=true
    networks: [edge]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    labels:
      - "traefik.enable=true"
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    # No host ports published; access via cloudflared -> traefik
`)

	sb.WriteString(`  cloudflared:
    image: cloudflare/cloudflared:latest
    command: tunnel run
    volumes:
      - ./cloudflared:/etc/cloudflared
    networks: [edge]
    extra_hosts:
      - "host.docker.internal:host-gateway"
`)

	sb.WriteString(fmt.Sprintf(`  whoami:
    image: traefik/whoami:v1.10
    networks: [edge]
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.whoami.rule=Host(`+"`%s`"+`)"
      - "traefik.http.services.whoami.loadbalancer.server.port=80"
`, whoamiHost))

	for _, svc := range s.Services {
		if !svc.Enabled {
			continue
		}
		appendService(&sb, svc, domain)
	}

	sb.WriteString("networks:\n  edge: {}\n")

	return os.WriteFile(path, []byte(strings.TrimSpace(sb.String())+"\n"), 0o600)
}

func writeCloudflared(path string, s state.State, hostnames []string) error {
	if len(hostnames) == 0 {
		hostnames = []string{"whoami.example.com"}
	}
	sort.Strings(hostnames)

	var sb strings.Builder
	tunnelID := s.Settings.Tunnel.TunnelID
	if tunnelID == "" {
		tunnelID = "YOUR_TUNNEL_ID"
	}
	sb.WriteString(fmt.Sprintf("tunnel: %s\n", tunnelID))
	if s.Settings.Tunnel.Mode == state.TunnelModeCredentialsFile && s.Settings.Tunnel.CredentialsFile != "" {
		sb.WriteString(fmt.Sprintf("credentials-file: %s\n", s.Settings.Tunnel.CredentialsFile))
	} else {
		sb.WriteString("credentials-file: /etc/cloudflared/credentials.json\n")
		if s.Settings.Tunnel.Token != "" {
			sb.WriteString(fmt.Sprintf("token: %s\n", s.Settings.Tunnel.Token))
		}
	}
	sb.WriteString("ingress:\n")
	uiHost := remoteUIHostname(s)
	apiHost := remoteAPIHostname(s)
	for _, h := range hostnames {
		service := "http://traefik:80"
		if uiHost != "" && strings.EqualFold(h, uiHost) {
			service = fmt.Sprintf("http://host.docker.internal:%s", uiProxyPort())
		} else if apiHost != "" && strings.EqualFold(h, apiHost) {
			service = fmt.Sprintf("http://host.docker.internal:%s", webhookProxyPort())
		}
		sb.WriteString(fmt.Sprintf("  - hostname: %s\n    service: %s\n", h, service))
	}
	sb.WriteString("  - service: http_status:404\n")
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

func writeTraefikDynamic(path string) error {
	content := strings.TrimSpace(`
http:
  middlewares: {}
  routers: {}
  services: {}
`)
	return os.WriteFile(path, []byte(content+"\n"), 0o600)
}

func appendService(sb *strings.Builder, svc state.Service, defaultDomain string) {
	name := sanitizeName(svc.Name)
	if name == "" {
		return
	}
	sb.WriteString(fmt.Sprintf("  %s:\n", name))
	sb.WriteString(fmt.Sprintf("    image: %s\n", svc.Image))
	sb.WriteString("    networks: [edge]\n")

	if len(svc.Env) > 0 {
		sb.WriteString("    environment:\n")
		keys := make([]string, 0, len(svc.Env))
		for k := range svc.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("      %s: %q\n", k, svc.Env[k]))
		}
	}

	if len(svc.Volumes) > 0 {
		sb.WriteString("    volumes:\n")
		for _, v := range svc.Volumes {
			sb.WriteString(fmt.Sprintf("      - %s\n", v))
		}
	}

	if svc.Healthcheck != nil {
		h := svc.Healthcheck
		sb.WriteString("    healthcheck:\n")
		if len(h.Command) > 0 {
			sb.WriteString(fmt.Sprintf("      test: [\"CMD\", %s]\n", quoteList(h.Command)))
		}
		if h.IntervalSeconds > 0 {
			sb.WriteString(fmt.Sprintf("      interval: %ds\n", h.IntervalSeconds))
		}
		if h.TimeoutSeconds > 0 {
			sb.WriteString(fmt.Sprintf("      timeout: %ds\n", h.TimeoutSeconds))
		}
		if h.Retries > 0 {
			sb.WriteString(fmt.Sprintf("      retries: %d\n", h.Retries))
		}
		if h.StartPeriodSeconds > 0 {
			sb.WriteString(fmt.Sprintf("      start_period: %ds\n", h.StartPeriodSeconds))
		}
	}

	if svc.Resources.MemoryLimitMB > 0 {
		sb.WriteString("    deploy:\n")
		sb.WriteString("      resources:\n")
		sb.WriteString("        limits:\n")
		sb.WriteString(fmt.Sprintf("          memory: %dm\n", svc.Resources.MemoryLimitMB))
	}

	labels := buildTraefikLabels(name, svc, defaultDomain)
	if len(labels) > 0 {
		sb.WriteString("    labels:\n")
		for _, l := range labels {
			sb.WriteString(fmt.Sprintf("      - %q\n", l))
		}
	}
}

func buildTraefikLabels(name string, svc state.Service, defaultDomain string) []string {
	var labels []string
	enable := "true"
	if !svc.Enabled {
		enable = "false"
	}
	labels = append(labels, fmt.Sprintf("traefik.enable=%s", enable))

	hosts := svc.Hostnames
	if len(hosts) == 0 && defaultDomain != "" {
		hosts = []string{fmt.Sprintf("%s.%s", name, defaultDomain)}
	}
	for i, h := range hosts {
		routerName := fmt.Sprintf("%s-%d", name, i)
		labels = append(labels, fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", routerName, h))
		labels = append(labels, fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", routerName))
		labels = append(labels, fmt.Sprintf("traefik.http.routers.%s.service=%s", routerName, name))
	}
	if svc.InternalPort > 0 {
		labels = append(labels, fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", name, svc.InternalPort))
	}
	return labels
}

func quoteList(items []string) string {
	var quoted []string
	for _, i := range items {
		quoted = append(quoted, fmt.Sprintf("%q", i))
	}
	return strings.Join(quoted, ", ")
}

var nameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

func sanitizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = nameSanitizer.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	return name
}

func uiProxyPort() string {
	addr := os.Getenv("TINYSERVE_UI_ADDR")
	if addr == "" {
		return "7071"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "7071"
	}
	return port
}

func webhookProxyPort() string {
	addr := os.Getenv("TINYSERVE_WEBHOOK_ADDR")
	if addr == "" {
		return "7072"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "7072"
	}
	return port
}

func remoteUIHostname(s state.State) string {
	if s.Settings.Remote.UIHostname != "" {
		return s.Settings.Remote.UIHostname
	}
	return s.Settings.Remote.Hostname
}

func remoteAPIHostname(s state.State) string {
	return s.Settings.Remote.APIHostname
}

func collectHostnames(s state.State) []string {
	domain := s.Settings.DefaultDomain
	if domain == "" {
		domain = "example.com"
	}
	var hosts []string
	hosts = append(hosts, "whoami."+domain)
	for _, svc := range s.Services {
		if !svc.Enabled {
			continue
		}
		if len(svc.Hostnames) > 0 {
			hosts = append(hosts, svc.Hostnames...)
		} else if domain != "" && svc.Name != "" {
			hosts = append(hosts, fmt.Sprintf("%s.%s", sanitizeName(svc.Name), domain))
		}
	}
	if s.Settings.Remote.Enabled && s.Settings.Remote.Hostname != "" {
		hosts = append(hosts, s.Settings.Remote.Hostname)
	}
	if s.Settings.Remote.Enabled {
		if ui := remoteUIHostname(s); ui != "" {
			hosts = append(hosts, ui)
		}
		if api := remoteAPIHostname(s); api != "" {
			hosts = append(hosts, api)
		}
	}
	return unique(hosts)
}

func unique(in []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, h := range in {
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}
