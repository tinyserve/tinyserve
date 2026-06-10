package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tinyserve/internal/backup"
	"tinyserve/internal/version"
)

func cmdBackup(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tinyserve backup <config|create|list|restore> ...")
	}
	switch args[0] {
	case "config":
		return cmdBackupConfig(args[1:])
	case "create":
		return cmdBackupCreate(args[1:])
	case "list":
		return cmdBackupList(args[1:])
	case "restore":
		return cmdBackupRestore(args[1:])
	default:
		return fmt.Errorf("unknown backup subcommand: %s", args[0])
	}
}

func cmdBackupConfig(args []string) error {
	path, err := backupConfigPath()
	if err != nil {
		return err
	}
	if len(args) == 0 || (len(args) == 1 && args[0] == "--show") {
		cfg, err := backup.LoadConfig(path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("backup is not configured; run tinyserve backup config --bucket BUCKET")
			}
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cfg.Redacted())
	}

	cfg, err := backup.LoadConfig(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "tinyserve-backups"
	}
	clearCredentials := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--bucket":
			i++
			if i >= len(args) {
				return fmt.Errorf("--bucket requires a value")
			}
			cfg.Bucket = args[i]
		case "--prefix":
			i++
			if i >= len(args) {
				return fmt.Errorf("--prefix requires a value")
			}
			cfg.Prefix = strings.Trim(args[i], "/")
		case "--endpoint":
			i++
			if i >= len(args) {
				return fmt.Errorf("--endpoint requires a value")
			}
			cfg.Endpoint = args[i]
		case "--region":
			i++
			if i >= len(args) {
				return fmt.Errorf("--region requires a value")
			}
			cfg.Region = args[i]
		case "--profile":
			i++
			if i >= len(args) {
				return fmt.Errorf("--profile requires a value")
			}
			cfg.Profile = args[i]
		case "--access-key":
			i++
			if i >= len(args) {
				return fmt.Errorf("--access-key requires a value")
			}
			cfg.AccessKeyID = args[i]
		case "--secret-key":
			i++
			if i >= len(args) {
				return fmt.Errorf("--secret-key requires a value")
			}
			cfg.SecretAccessKey = args[i]
		case "--clear-credentials":
			clearCredentials = true
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if clearCredentials {
		cfg.AccessKeyID = ""
		cfg.SecretAccessKey = ""
	}
	if err := backup.SaveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Printf("Backup config saved: %s\n", path)
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		fmt.Println("Credentials: AWS environment, profile, or default credential chain")
	} else {
		fmt.Println("Credentials: stored in backup config file")
	}
	return nil
}

func cmdBackupCreate(args []string) error {
	opts := backup.CreateOptions{
		Type: backup.KindPartial,
	}
	upload := true
	typeSet := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--partial":
			if typeSet && opts.Type != backup.KindPartial {
				return fmt.Errorf("choose only one of --partial or --full")
			}
			opts.Type = backup.KindPartial
			typeSet = true
		case "--full":
			if typeSet && opts.Type != backup.KindFull {
				return fmt.Errorf("choose only one of --partial or --full")
			}
			opts.Type = backup.KindFull
			typeSet = true
		case "--output":
			i++
			if i >= len(args) {
				return fmt.Errorf("--output requires a value")
			}
			opts.OutputDir = args[i]
		case "--no-upload":
			upload = false
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	dataRoot, err := tinyserveDataRoot()
	if err != nil {
		return err
	}
	opts.DataRoot = dataRoot
	opts.Version = version.String()

	var cfg backup.Config
	if upload {
		cfg, err = loadBackupConfig()
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("backup upload is not configured; run tinyserve backup config --bucket BUCKET or pass --no-upload")
			}
			return err
		}
	}

	result, err := backup.Create(context.Background(), opts)
	if err != nil {
		return err
	}

	resp := map[string]any{
		"status":        "created",
		"type":          result.Manifest.Type,
		"timestamp":     result.Manifest.Timestamp,
		"artifact_path": result.ArtifactPath,
	}
	if len(result.Manifest.Warnings) > 0 {
		resp["warnings"] = result.Manifest.Warnings
	}
	if upload {
		uri := s3ArtifactURI(cfg, result.Manifest.Type, result.Manifest.Timestamp, filepath.Base(result.ArtifactPath))
		if err := uploadBackupArtifact(cfg, result.ArtifactPath, uri); err != nil {
			return err
		}
		resp["s3_uri"] = uri
		resp["status"] = "uploaded"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func cmdBackupList(args []string) error {
	kinds := []backup.Kind{backup.KindPartial, backup.KindFull}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--partial":
			kinds = []backup.Kind{backup.KindPartial}
		case "--full":
			kinds = []backup.Kind{backup.KindFull}
		case "--all":
			kinds = []backup.Kind{backup.KindPartial, backup.KindFull}
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	cfg, err := loadBackupConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("backup is not configured; run tinyserve backup config --bucket BUCKET")
		}
		return err
	}

	var refs []backupRef
	for _, kind := range kinds {
		found, err := listBackupRefs(cfg, kind)
		if err != nil {
			return err
		}
		refs = append(refs, found...)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Timestamp == refs[j].Timestamp {
			return refs[i].Type < refs[j].Type
		}
		return refs[i].Timestamp < refs[j].Timestamp
	})
	if len(refs) == 0 {
		fmt.Println("No backups found")
		return nil
	}
	fmt.Printf("%-8s %-22s %s\n", "TYPE", "TIMESTAMP", "URI")
	fmt.Println(strings.Repeat("-", 88))
	for _, ref := range refs {
		fmt.Printf("%-8s %-22s %s\n", ref.Type, ref.Timestamp, ref.URI)
	}
	return nil
}

func cmdBackupRestore(args []string) error {
	var timestamp string
	var artifactPath string
	var kind backup.Kind
	var kindSet bool
	var force bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--partial":
			if kindSet && kind != backup.KindPartial {
				return fmt.Errorf("choose only one of --partial or --full")
			}
			kind = backup.KindPartial
			kindSet = true
		case "--full":
			if kindSet && kind != backup.KindFull {
				return fmt.Errorf("choose only one of --partial or --full")
			}
			kind = backup.KindFull
			kindSet = true
		case "--artifact":
			i++
			if i >= len(args) {
				return fmt.Errorf("--artifact requires a value")
			}
			artifactPath = args[i]
		case "--force":
			force = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s", args[i])
			}
			if timestamp != "" {
				return fmt.Errorf("unexpected argument: %s", args[i])
			}
			timestamp = args[i]
		}
	}
	if artifactPath == "" && timestamp == "" {
		return fmt.Errorf("usage: tinyserve backup restore <timestamp> [--partial | --full] [--force]\n   or: tinyserve backup restore --artifact PATH [--force]")
	}
	if artifactPath != "" && timestamp != "" {
		return fmt.Errorf("pass either a timestamp or --artifact, not both")
	}
	if !force && daemonReachable() {
		return fmt.Errorf("tinyserved is running; stop it before restore or pass --force to bypass this check")
	}

	cleanup := func() {}
	if artifactPath == "" {
		cfg, err := loadBackupConfig()
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("backup is not configured; run tinyserve backup config --bucket BUCKET")
			}
			return err
		}
		if !kindSet {
			detected, err := detectBackupType(cfg, timestamp)
			if err != nil {
				return err
			}
			kind = detected
		}
		downloaded, cleanupFn, err := downloadBackupArtifact(cfg, kind, timestamp)
		if err != nil {
			return err
		}
		artifactPath = downloaded
		cleanup = cleanupFn
	}
	defer cleanup()

	dataRoot, err := tinyserveDataRoot()
	if err != nil {
		return err
	}
	result, err := backup.Restore(context.Background(), backup.RestoreOptions{
		DataRoot:        dataRoot,
		ArtifactPath:    artifactPath,
		SafetyBackup:    true,
		SafetyOutputDir: filepath.Join(dataRoot, "backups"),
		Version:         version.String(),
	})
	if err != nil {
		return err
	}
	resp := map[string]any{
		"status":    "restored",
		"type":      result.Manifest.Type,
		"timestamp": result.Manifest.Timestamp,
	}
	if result.SafetyArtifact != "" {
		resp["safety_artifact"] = result.SafetyArtifact
	}
	if len(result.Manifest.Warnings) > 0 {
		resp["warnings"] = result.Manifest.Warnings
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

type backupRef struct {
	Type      backup.Kind
	Timestamp string
	URI       string
}

func tinyserveDataRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "tinyserve"), nil
}

func backupConfigPath() (string, error) {
	root, err := tinyserveDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "backup-config.json"), nil
}

func loadBackupConfig() (backup.Config, error) {
	path, err := backupConfigPath()
	if err != nil {
		return backup.Config{}, err
	}
	return backup.LoadConfig(path)
}

func uploadBackupArtifact(cfg backup.Config, artifactPath, uri string) error {
	_, err := runAWS(cfg, "s3", "cp", artifactPath, uri, "--only-show-errors")
	return err
}

func listBackupRefs(cfg backup.Config, kind backup.Kind) ([]backupRef, error) {
	out, err := runAWS(cfg, "s3", "ls", s3TypeURI(cfg, kind)+"/")
	if err != nil {
		return nil, err
	}
	var refs []backupRef
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		preIndex := -1
		for i, field := range fields {
			if field == "PRE" {
				preIndex = i
				break
			}
		}
		if preIndex == -1 || preIndex+1 >= len(fields) {
			continue
		}
		ts := strings.TrimSuffix(fields[preIndex+1], "/")
		if ts == "" {
			continue
		}
		refs = append(refs, backupRef{
			Type:      kind,
			Timestamp: ts,
			URI:       s3BackupURI(cfg, kind, ts),
		})
	}
	return refs, nil
}

func detectBackupType(cfg backup.Config, timestamp string) (backup.Kind, error) {
	var matches []backup.Kind
	for _, kind := range []backup.Kind{backup.KindPartial, backup.KindFull} {
		if backupExists(cfg, kind, timestamp) {
			matches = append(matches, kind)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("backup %s not found in partial or full backups", timestamp)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("backup %s exists as both partial and full; pass --partial or --full", timestamp)
	}
	return matches[0], nil
}

func backupExists(cfg backup.Config, kind backup.Kind, timestamp string) bool {
	out, err := runAWS(cfg, "s3", "ls", s3BackupURI(cfg, kind, timestamp)+"/")
	return err == nil && strings.TrimSpace(out) != ""
}

func downloadBackupArtifact(cfg backup.Config, kind backup.Kind, timestamp string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "tinyserve-backup-download-*")
	if err != nil {
		return "", nil, fmt.Errorf("create download dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	_, err = runAWS(cfg, "s3", "cp", s3BackupURI(cfg, kind, timestamp)+"/", tmpDir, "--recursive", "--only-show-errors")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	matches, err := filepath.Glob(filepath.Join(tmpDir, "*.tar.gz"))
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if len(matches) == 0 {
		cleanup()
		return "", nil, fmt.Errorf("downloaded backup %s/%s contains no .tar.gz artifact", kind, timestamp)
	}
	sort.Strings(matches)
	return matches[len(matches)-1], cleanup, nil
}

func runAWS(cfg backup.Config, args ...string) (string, error) {
	if _, err := exec.LookPath("aws"); err != nil {
		return "", fmt.Errorf("aws CLI not found in PATH")
	}
	fullArgs := []string{}
	if cfg.Endpoint != "" {
		fullArgs = append(fullArgs, "--endpoint-url", cfg.Endpoint)
	}
	if cfg.Region != "" {
		fullArgs = append(fullArgs, "--region", cfg.Region)
	}
	if cfg.Profile != "" {
		fullArgs = append(fullArgs, "--profile", cfg.Profile)
	}
	fullArgs = append(fullArgs, args...)

	cmd := exec.Command("aws", fullArgs...)
	cmd.Env = os.Environ()
	if cfg.AccessKeyID != "" {
		cmd.Env = append(cmd.Env, "AWS_ACCESS_KEY_ID="+cfg.AccessKeyID)
	}
	if cfg.SecretAccessKey != "" {
		cmd.Env = append(cmd.Env, "AWS_SECRET_ACCESS_KEY="+cfg.SecretAccessKey)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("aws %s: %w\n%s", strings.Join(fullArgs, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func s3ArtifactURI(cfg backup.Config, kind backup.Kind, timestamp, artifactName string) string {
	return s3BackupURI(cfg, kind, timestamp) + "/" + artifactName
}

func s3BackupURI(cfg backup.Config, kind backup.Kind, timestamp string) string {
	return s3TypeURI(cfg, kind) + "/" + timestamp
}

func s3TypeURI(cfg backup.Config, kind backup.Kind) string {
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix == "" {
		return fmt.Sprintf("s3://%s/%s", cfg.Bucket, kind)
	}
	return fmt.Sprintf("s3://%s/%s/%s", cfg.Bucket, prefix, kind)
}

func daemonReachable() bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(apiBase() + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}
