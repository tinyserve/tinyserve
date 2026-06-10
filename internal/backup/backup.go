package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Kind string

const (
	KindPartial Kind = "partial"
	KindFull    Kind = "full"
)

const manifestVersion = 1

type Config struct {
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	Endpoint        string `json:"endpoint,omitempty"`
	Region          string `json:"region,omitempty"`
	Profile         string `json:"profile,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

type Manifest struct {
	Version          int             `json:"version"`
	Type             Kind            `json:"type"`
	Timestamp        string          `json:"timestamp"`
	CreatedAt        string          `json:"created_at"`
	Hostname         string          `json:"hostname,omitempty"`
	TinyServeVersion string          `json:"tinyserve_version,omitempty"`
	Entries          []ManifestEntry `json:"entries"`
	Warnings         []string        `json:"warnings,omitempty"`
}

type ManifestEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type CreateOptions struct {
	DataRoot  string
	OutputDir string
	Type      Kind
	Now       time.Time
	Version   string
}

type CreateResult struct {
	ArtifactPath string
	Manifest     Manifest
}

type RestoreOptions struct {
	DataRoot        string
	ArtifactPath    string
	SafetyBackup    bool
	SafetyOutputDir string
	Now             time.Time
	Version         string
}

type RestoreResult struct {
	Manifest       Manifest
	SafetyArtifact string
}

func SaveConfig(path string, cfg Config) error {
	if cfg.Prefix == "" {
		cfg.Prefix = "tinyserve-backups"
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "tinyserve-backups"
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Bucket == "" {
		return errors.New("bucket is required")
	}
	if strings.Contains(c.Bucket, "/") {
		return fmt.Errorf("bucket must not contain slashes: %q", c.Bucket)
	}
	if c.Prefix == "" {
		return errors.New("prefix is required")
	}
	if strings.HasPrefix(c.Prefix, "/") {
		return fmt.Errorf("prefix must be relative: %q", c.Prefix)
	}
	return nil
}

func (c Config) Redacted() Config {
	if c.AccessKeyID != "" {
		c.AccessKeyID = redact(c.AccessKeyID)
	}
	if c.SecretAccessKey != "" {
		c.SecretAccessKey = "********"
	}
	return c
}

func Create(ctx context.Context, opts CreateOptions) (CreateResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateResult{}, err
	}
	if opts.DataRoot == "" {
		return CreateResult{}, errors.New("data root is required")
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join(opts.DataRoot, "backups")
	}
	if opts.Type == "" {
		opts.Type = KindPartial
	}
	if opts.Type != KindPartial && opts.Type != KindFull {
		return CreateResult{}, fmt.Errorf("unsupported backup type: %s", opts.Type)
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	} else {
		opts.Now = opts.Now.UTC()
	}

	if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
		return CreateResult{}, fmt.Errorf("create output dir: %w", err)
	}

	workDir, err := os.MkdirTemp("", "tinyserve-backup-*")
	if err != nil {
		return CreateResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	stateSnapshot := filepath.Join(workDir, "state.db")
	if err := SnapshotSQLite(ctx, filepath.Join(opts.DataRoot, "state.db"), stateSnapshot); err != nil {
		return CreateResult{}, fmt.Errorf("snapshot sqlite: %w", err)
	}

	hostname, _ := os.Hostname()
	timestamp := opts.Now.Format("2006-01-02T15-04-05Z")
	manifest := Manifest{
		Version:          manifestVersion,
		Type:             opts.Type,
		Timestamp:        timestamp,
		CreatedAt:        opts.Now.Format(time.RFC3339),
		Hostname:         hostname,
		TinyServeVersion: opts.Version,
	}

	sources := []archiveSource{
		{src: stateSnapshot, dst: "state.db"},
	}
	sources = appendIfExists(sources, filepath.Join(opts.DataRoot, "generated", "current"), "generated/current")
	sources = appendIfExists(sources, filepath.Join(opts.DataRoot, "cloudflared"), "cloudflared")
	sources = appendIfExists(sources, filepath.Join(opts.DataRoot, "traefik"), "traefik")
	if opts.Type == KindFull {
		sources = appendIfExists(sources, filepath.Join(opts.DataRoot, "services"), "services")
		manifest.Warnings = append(manifest.Warnings, externalVolumeWarnings(ctx, stateSnapshot, opts.DataRoot)...)
	}

	entries, err := collectEntries(sources)
	if err != nil {
		return CreateResult{}, err
	}
	manifest.Entries = entries

	artifactName := fmt.Sprintf("tinyserve-backup-%s-%s.tar.gz", opts.Type, timestamp)
	artifactPath := filepath.Join(opts.OutputDir, artifactName)
	if err := writeArtifact(artifactPath, sources, manifest); err != nil {
		return CreateResult{}, err
	}

	return CreateResult{
		ArtifactPath: artifactPath,
		Manifest:     manifest,
	}, nil
}

func SnapshotSQLite(ctx context.Context, srcPath, dstPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("stat source database: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	_ = os.Remove(dstPath)

	db, err := sql.Open("sqlite", srcPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "VACUUM INTO "+sqliteString(dstPath)); err != nil {
		return fmt.Errorf("vacuum into snapshot: %w", err)
	}
	return nil
}

func ReadManifest(artifactPath string) (Manifest, error) {
	file, err := os.Open(artifactPath)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Name != "manifest.json" {
			continue
		}
		var manifest Manifest
		if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
			return Manifest{}, fmt.Errorf("decode manifest: %w", err)
		}
		return manifest, nil
	}
	return Manifest{}, errors.New("manifest.json not found in artifact")
}

func Restore(ctx context.Context, opts RestoreOptions) (RestoreResult, error) {
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}
	if opts.DataRoot == "" {
		return RestoreResult{}, errors.New("data root is required")
	}
	if opts.ArtifactPath == "" {
		return RestoreResult{}, errors.New("artifact path is required")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	} else {
		opts.Now = opts.Now.UTC()
	}

	manifest, err := ReadManifest(opts.ArtifactPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if manifest.Version != manifestVersion {
		return RestoreResult{}, fmt.Errorf("unsupported manifest version: %d", manifest.Version)
	}
	if manifest.Type != KindPartial && manifest.Type != KindFull {
		return RestoreResult{}, fmt.Errorf("unsupported backup type: %s", manifest.Type)
	}

	var safetyArtifact string
	if opts.SafetyBackup {
		if _, err := os.Stat(filepath.Join(opts.DataRoot, "state.db")); err == nil {
			createOpts := CreateOptions{
				DataRoot:  opts.DataRoot,
				OutputDir: opts.SafetyOutputDir,
				Type:      KindFull,
				Now:       opts.Now,
				Version:   opts.Version,
			}
			result, err := Create(ctx, createOpts)
			if err != nil {
				return RestoreResult{}, fmt.Errorf("create safety backup: %w", err)
			}
			safetyArtifact = result.ArtifactPath
		}
	}

	extractDir, err := os.MkdirTemp("", "tinyserve-restore-*")
	if err != nil {
		return RestoreResult{}, fmt.Errorf("create restore dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	if err := extractArtifact(opts.ArtifactPath, extractDir); err != nil {
		return RestoreResult{}, err
	}
	if _, err := os.Stat(filepath.Join(extractDir, "state.db")); err != nil {
		return RestoreResult{}, fmt.Errorf("artifact missing state.db: %w", err)
	}

	if err := os.MkdirAll(opts.DataRoot, 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("create data root: %w", err)
	}

	if err := restoreFile(filepath.Join(extractDir, "state.db"), filepath.Join(opts.DataRoot, "state.db")); err != nil {
		return RestoreResult{}, err
	}
	_ = os.Remove(filepath.Join(opts.DataRoot, "state.db-wal"))
	_ = os.Remove(filepath.Join(opts.DataRoot, "state.db-shm"))

	for _, rel := range []string{"generated/current", "cloudflared", "traefik", "services"} {
		src := filepath.Join(extractDir, filepath.FromSlash(rel))
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(opts.DataRoot, filepath.FromSlash(rel))
		if err := os.RemoveAll(dst); err != nil {
			return RestoreResult{}, fmt.Errorf("remove %s: %w", rel, err)
		}
		if err := copyTree(src, dst); err != nil {
			return RestoreResult{}, fmt.Errorf("restore %s: %w", rel, err)
		}
	}

	return RestoreResult{
		Manifest:       manifest,
		SafetyArtifact: safetyArtifact,
	}, nil
}

type archiveSource struct {
	src string
	dst string
}

func appendIfExists(sources []archiveSource, src, dst string) []archiveSource {
	if _, err := os.Stat(src); err == nil {
		return append(sources, archiveSource{src: src, dst: dst})
	}
	return sources
}

func collectEntries(sources []archiveSource) ([]ManifestEntry, error) {
	var entries []ManifestEntry
	for _, source := range sources {
		if err := walkSource(source, func(srcPath, archivePath string, info fs.FileInfo) error {
			entry := ManifestEntry{
				Path: path.Clean(archivePath),
				Type: entryType(info),
			}
			if info.Mode().IsRegular() {
				entry.Size = info.Size()
				sum, err := fileSHA256(srcPath)
				if err != nil {
					return err
				}
				entry.SHA256 = sum
			}
			entries = append(entries, entry)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

func writeArtifact(artifactPath string, sources []archiveSource, manifest Manifest) error {
	tmpPath := artifactPath + ".tmp"
	_ = os.Remove(tmpPath)

	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	success := false
	defer func() {
		file.Close()
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		tw.Close()
		gz.Close()
		return fmt.Errorf("encode manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	if err := tw.WriteHeader(&tar.Header{
		Name:    "manifest.json",
		Mode:    0o600,
		Size:    int64(len(manifestData)),
		ModTime: time.Now().UTC(),
	}); err != nil {
		tw.Close()
		gz.Close()
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(manifestData); err != nil {
		tw.Close()
		gz.Close()
		return fmt.Errorf("write manifest: %w", err)
	}

	for _, source := range sources {
		if err := walkSource(source, func(srcPath, archivePath string, info fs.FileInfo) error {
			return addTarEntry(tw, srcPath, path.Clean(archivePath), info)
		}); err != nil {
			tw.Close()
			gz.Close()
			return err
		}
	}

	if err := tw.Close(); err != nil {
		gz.Close()
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close artifact: %w", err)
	}
	if err := os.Rename(tmpPath, artifactPath); err != nil {
		return fmt.Errorf("promote artifact: %w", err)
	}
	success = true
	return nil
}

func walkSource(source archiveSource, fn func(srcPath, archivePath string, info fs.FileInfo) error) error {
	info, err := os.Lstat(source.src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", source.src, err)
	}
	if !info.IsDir() {
		return fn(source.src, source.dst, info)
	}
	return filepath.WalkDir(source.src, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source.src, srcPath)
		if err != nil {
			return err
		}
		archivePath := source.dst
		if rel != "." {
			archivePath = path.Join(source.dst, filepath.ToSlash(rel))
		}
		return fn(srcPath, archivePath, info)
	})
}

func addTarEntry(tw *tar.Writer, srcPath, archivePath string, info fs.FileInfo) error {
	var link string
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(srcPath)
		if err != nil {
			return fmt.Errorf("readlink %s: %w", srcPath, err)
		}
		link = target
	}
	hdr, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return fmt.Errorf("create tar header %s: %w", srcPath, err)
	}
	hdr.Name = archivePath
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header %s: %w", archivePath, err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer file.Close()
	if _, err := io.Copy(tw, file); err != nil {
		return fmt.Errorf("write tar file %s: %w", archivePath, err)
	}
	return nil
}

func extractArtifact(artifactPath, dstRoot string) error {
	file, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if hdr.Name == "manifest.json" {
			continue
		}
		dst, err := safeJoin(dstRoot, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, fs.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("create dir %s: %w", hdr.Name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return fmt.Errorf("create parent %s: %w", hdr.Name, err)
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", hdr.Name, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("extract file %s: %w", hdr.Name, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("close file %s: %w", hdr.Name, err)
			}
		case tar.TypeSymlink:
			if !safeLinkTarget(hdr.Linkname) {
				return fmt.Errorf("unsafe symlink target for %s: %s", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return fmt.Errorf("create symlink parent %s: %w", hdr.Name, err)
			}
			_ = os.Remove(dst)
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return fmt.Errorf("create symlink %s: %w", hdr.Name, err)
			}
		default:
			return fmt.Errorf("unsupported tar entry %s type %d", hdr.Name, hdr.Typeflag)
		}
	}
}

func restoreFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	tmp := dst + ".restore-tmp"
	if err := copyFile(src, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod restore file: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("promote restore file: %w", err)
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, srcPath)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			if !safeLinkTarget(target) {
				return fmt.Errorf("unsafe symlink target %s: %s", srcPath, target)
			}
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
				return err
			}
			return os.Symlink(target, dstPath)
		}
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(srcPath, dstPath)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func externalVolumeWarnings(ctx context.Context, dbPath, dataRoot string) []string {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return []string{fmt.Sprintf("could not inspect service volumes: %v", err)}
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, "SELECT name, volumes FROM services")
	if err != nil {
		return []string{fmt.Sprintf("could not inspect service volumes: %v", err)}
	}
	defer rows.Close()

	var warnings []string
	dataRootClean, err := filepath.EvalSymlinks(dataRoot)
	if err != nil {
		dataRootClean = filepath.Clean(dataRoot)
	}
	for rows.Next() {
		var serviceName string
		var volumesRaw sql.NullString
		if err := rows.Scan(&serviceName, &volumesRaw); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not scan service volume row: %v", err))
			continue
		}
		if !volumesRaw.Valid || volumesRaw.String == "" {
			continue
		}
		var volumes []string
		if err := json.Unmarshal([]byte(volumesRaw.String), &volumes); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not parse volumes for service %s: %v", serviceName, err))
			continue
		}
		for _, volume := range volumes {
			hostPath := volumeHostPath(volume)
			if hostPath == "" || !filepath.IsAbs(hostPath) {
				continue
			}
			resolved, err := filepath.EvalSymlinks(hostPath)
			if err != nil {
				resolved = filepath.Clean(hostPath)
			}
			if !pathWithin(resolved, dataRootClean) {
				warnings = append(warnings, fmt.Sprintf("service %s volume %s is outside tinyserve data root and is not included", serviceName, hostPath))
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}

func volumeHostPath(volume string) string {
	parts := strings.SplitN(volume, ":", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func entryType(info fs.FileInfo) string {
	switch {
	case info.IsDir():
		return "dir"
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	case info.Mode().IsRegular():
		return "file"
	default:
		return "other"
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open for sha256 %s: %w", path, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("sha256 %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeJoin(root, name string) (string, error) {
	clean := path.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	dst := filepath.Join(root, filepath.FromSlash(clean))
	rootClean := filepath.Clean(root)
	dstClean := filepath.Clean(dst)
	if !pathWithin(dstClean, rootClean) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	return dst, nil
}

func safeLinkTarget(target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return false
	}
	clean := path.Clean(filepath.ToSlash(target))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func pathWithin(pathValue, root string) bool {
	rel, err := filepath.Rel(root, pathValue)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func sqliteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func redact(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + strings.Repeat("*", len(value)-4) + value[len(value)-2:]
}
