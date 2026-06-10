package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tinyserve/internal/state"
)

func TestCreatePartialBackup(t *testing.T) {
	root := setupDataRoot(t)
	result, err := Create(context.Background(), CreateOptions{
		DataRoot:  root,
		OutputDir: filepath.Join(root, "backups"),
		Type:      KindPartial,
		Now:       fixedTime(),
		Version:   "test-version",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := os.Stat(result.ArtifactPath); err != nil {
		t.Fatalf("artifact not created: %v", err)
	}

	manifest, err := ReadManifest(result.ArtifactPath)
	if err != nil {
		t.Fatalf("ReadManifest() error = %v", err)
	}
	if manifest.Type != KindPartial {
		t.Fatalf("manifest.Type = %q, want %q", manifest.Type, KindPartial)
	}
	if !hasEntry(manifest, "state.db") {
		t.Fatal("manifest missing state.db")
	}
	if !hasEntry(manifest, "generated/current/docker-compose.yml") {
		t.Fatal("manifest missing generated/current/docker-compose.yml")
	}
	if hasEntryPrefix(manifest, "services/") {
		t.Fatal("partial backup should not include services/")
	}
}

func TestCreateFullBackupAndRestore(t *testing.T) {
	root := setupDataRoot(t)
	result, err := Create(context.Background(), CreateOptions{
		DataRoot:  root,
		OutputDir: filepath.Join(root, "backups"),
		Type:      KindFull,
		Now:       fixedTime(),
		Version:   "test-version",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !hasEntry(result.Manifest, "services/app/data/file.txt") {
		t.Fatal("full backup missing managed service data")
	}
	if len(result.Manifest.Warnings) == 0 {
		t.Fatal("full backup should warn about external volumes")
	}
	if !strings.Contains(result.Manifest.Warnings[0], "outside tinyserve data root") {
		t.Fatalf("unexpected warning: %q", result.Manifest.Warnings[0])
	}

	restoreRoot := t.TempDir()
	restoreResult, err := Restore(context.Background(), RestoreOptions{
		DataRoot:     restoreRoot,
		ArtifactPath: result.ArtifactPath,
		SafetyBackup: false,
		Now:          fixedTime(),
		Version:      "test-version",
	})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restoreResult.Manifest.Timestamp != result.Manifest.Timestamp {
		t.Fatalf("restored timestamp = %q, want %q", restoreResult.Manifest.Timestamp, result.Manifest.Timestamp)
	}

	store, err := state.NewSQLiteStore(filepath.Join(restoreRoot, "state.db"))
	if err != nil {
		t.Fatalf("open restored sqlite: %v", err)
	}
	defer store.Close()
	st, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load restored state: %v", err)
	}
	if len(st.Services) != 1 || st.Services[0].Name != "app" {
		t.Fatalf("restored services = %+v", st.Services)
	}
	data, err := os.ReadFile(filepath.Join(restoreRoot, "services", "app", "data", "file.txt"))
	if err != nil {
		t.Fatalf("read restored service data: %v", err)
	}
	if string(data) != "service data\n" {
		t.Fatalf("restored service data = %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(restoreRoot, "generated", "current", "docker-compose.yml")); err != nil {
		t.Fatalf("generated current not restored: %v", err)
	}
}

func TestSaveLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup-config.json")
	cfg := Config{
		Bucket:          "bucket",
		Prefix:          "prefix",
		Endpoint:        "https://example.invalid",
		AccessKeyID:     "ABCD1234",
		SecretAccessKey: "secret",
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded.Bucket != cfg.Bucket || loaded.SecretAccessKey != cfg.SecretAccessKey {
		t.Fatalf("loaded config = %+v, want %+v", loaded, cfg)
	}
	redacted := loaded.Redacted()
	if redacted.SecretAccessKey == loaded.SecretAccessKey {
		t.Fatal("Redacted() did not redact secret access key")
	}
	if redacted.AccessKeyID == loaded.AccessKeyID {
		t.Fatal("Redacted() did not redact access key id")
	}
}

func setupDataRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		"generated/current",
		"cloudflared",
		"traefik",
		"services/app/data",
		"backups",
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	writeTestFile(t, filepath.Join(root, "generated", "current", "docker-compose.yml"), "services: {}\n")
	writeTestFile(t, filepath.Join(root, "cloudflared", "config.yml"), "tunnel: test\n")
	writeTestFile(t, filepath.Join(root, "traefik", "dynamic.yml"), "http: {}\n")
	writeTestFile(t, filepath.Join(root, "services", "app", "data", "file.txt"), "service data\n")

	external := filepath.Join(t.TempDir(), "external")
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatalf("create external volume: %v", err)
	}

	store, err := state.NewSQLiteStore(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()
	st := state.NewState()
	st.Services = []state.Service{
		{
			ID:           "svc-1",
			Name:         "app",
			Type:         state.ServiceTypeRegistryImage,
			Image:        "nginx:latest",
			InternalPort: 80,
			Enabled:      true,
			Volumes: []string{
				filepath.Join(root, "services", "app", "data") + ":/data",
				external + ":/external",
			},
		},
	}
	if err := store.Save(context.Background(), st); err != nil {
		t.Fatalf("save state: %v", err)
	}
	return root
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
}

func hasEntry(manifest Manifest, entryPath string) bool {
	for _, entry := range manifest.Entries {
		if entry.Path == entryPath {
			return true
		}
	}
	return false
}

func hasEntryPrefix(manifest Manifest, prefix string) bool {
	for _, entry := range manifest.Entries {
		if strings.HasPrefix(entry.Path, prefix) {
			return true
		}
	}
	return false
}
