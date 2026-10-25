// Package catalog installs explicitly versioned Integration extension
// executables from a trusted local or HTTPS catalog. Spinner itself never
// downloads or updates executable code while serving traffic.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1
const maxCatalogBytes int64 = 1 << 20
const maxArtifactBytes int64 = 128 << 20

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[a-z0-9][a-z0-9.-]*)?$`)
var platformPattern = regexp.MustCompile(`^[a-z0-9]+$`)

type Catalog struct {
	SchemaVersion int        `json:"schema_version"`
	Artifacts     []Artifact `json:"artifacts"`
}

type Artifact struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

type Receipt struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	SHA256  string `json:"sha256"`
}

type InstallRequest struct {
	Catalog   string
	Name      string
	Version   string
	Dir       string
	Client    *http.Client
	PublicKey []byte
}

// CreateLocal writes a catalog for already-built executables in one directory.
// The artifacts are referenced by filename so the directory can be copied or
// hosted unchanged. It refuses to overwrite an existing catalog.
func CreateLocal(dir, version string) (string, error) {
	if !versionPattern.MatchString(version) {
		return "", errors.New("catalog requires a vMAJOR.MINOR.PATCH version")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return "", err
	}
	manifest := Catalog{SchemaVersion: SchemaVersion}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "gateway-") {
			continue
		}
		name := strings.TrimPrefix(entry.Name(), "gateway-")
		if !namePattern.MatchString(name) {
			return "", fmt.Errorf("invalid bundled integration name %q", name)
		}
		path := filepath.Join(absolute, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Size() == 0 || info.Size() > maxArtifactBytes {
			return "", fmt.Errorf("bundled integration %q must be a bounded executable regular file", entry.Name())
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return "", err
		}
		manifest.Artifacts = append(manifest.Artifacts, Artifact{
			Name: name, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH,
			URL: entry.Name(), SHA256: digest,
		})
	}
	if len(manifest.Artifacts) == 0 {
		return "", errors.New("catalog directory contains no gateway-* integration executables")
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool { return manifest.Artifacts[i].Name < manifest.Artifacts[j].Name })
	return writeCatalogManifest(absolute, manifest)
}

// CreatePlatformBundle catalogs binaries laid out as OS/architecture/gateway-*
// beneath one directory. It permits a release to contain multiple targets
// while preserving relative URLs for both local and HTTPS installation.
func CreatePlatformBundle(dir, version string) (string, error) {
	if !versionPattern.MatchString(version) {
		return "", errors.New("catalog requires a vMAJOR.MINOR.PATCH version")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	systems, err := os.ReadDir(absolute)
	if err != nil {
		return "", err
	}
	manifest := Catalog{SchemaVersion: SchemaVersion}
	for _, system := range systems {
		if !system.IsDir() {
			continue
		}
		if !platformPattern.MatchString(system.Name()) {
			return "", fmt.Errorf("invalid integration OS directory %q", system.Name())
		}
		systemPath := filepath.Join(absolute, system.Name())
		architectures, err := os.ReadDir(systemPath)
		if err != nil {
			return "", err
		}
		for _, architecture := range architectures {
			if !architecture.IsDir() {
				continue
			}
			if !platformPattern.MatchString(architecture.Name()) {
				return "", fmt.Errorf("invalid integration architecture directory %q", architecture.Name())
			}
			architecturePath := filepath.Join(systemPath, architecture.Name())
			entries, err := os.ReadDir(architecturePath)
			if err != nil {
				return "", err
			}
			count := 0
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), "gateway-") {
					continue
				}
				name := strings.TrimPrefix(entry.Name(), "gateway-")
				if !namePattern.MatchString(name) {
					return "", fmt.Errorf("invalid bundled integration name %q", name)
				}
				path := filepath.Join(architecturePath, entry.Name())
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Size() == 0 || info.Size() > maxArtifactBytes {
					return "", fmt.Errorf("bundled integration %q must be a bounded executable regular file", path)
				}
				digest, err := fileSHA256(path)
				if err != nil {
					return "", err
				}
				manifest.Artifacts = append(manifest.Artifacts, Artifact{
					Name: name, Version: version, OS: system.Name(), Arch: architecture.Name(),
					URL: filepath.ToSlash(filepath.Join(system.Name(), architecture.Name(), entry.Name())), SHA256: digest,
				})
				count++
			}
			if count == 0 {
				return "", fmt.Errorf("integration platform directory %s/%s contains no gateway-* executables", system.Name(), architecture.Name())
			}
		}
	}
	if len(manifest.Artifacts) == 0 {
		return "", errors.New("catalog directory contains no platform integration executables")
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool {
		a, b := manifest.Artifacts[i], manifest.Artifacts[j]
		return a.Name+"\x00"+a.OS+"\x00"+a.Arch < b.Name+"\x00"+b.OS+"\x00"+b.Arch
	})
	return writeCatalogManifest(absolute, manifest)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeCatalogManifest(absolute string, manifest Catalog) (string, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(absolute, "catalog.json")
	if err := writeSyncedFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write integration catalog: %w", err)
	}
	if err := syncDirectory(absolute); err != nil {
		return "", err
	}
	return path, nil
}

// Read fetches and validates a catalog. A remote catalog and all redirects
// must use HTTPS; a local catalog may reference files beneath its directory.
func Read(ctx context.Context, reference string, client *http.Client) (Catalog, error) {
	contents, err := readSourceBytes(ctx, reference, client, maxCatalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	return decodeCatalog(contents)
}

func readSourceBytes(ctx context.Context, reference string, client *http.Client, limit int64) ([]byte, error) {
	reader, err := openSource(ctx, reference, client, limit)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	contents, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read integration source: %w", err)
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("integration source exceeds its size limit")
	}
	return contents, nil
}

func decodeCatalog(contents []byte) (Catalog, error) {
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode integration catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Catalog{}, errors.New("integration catalog contains trailing data or exceeds its size limit")
	}
	if catalog.SchemaVersion != SchemaVersion {
		return Catalog{}, fmt.Errorf("unsupported integration catalog schema %d", catalog.SchemaVersion)
	}
	seen := make(map[string]bool, len(catalog.Artifacts))
	for _, artifact := range catalog.Artifacts {
		if !namePattern.MatchString(artifact.Name) || !versionPattern.MatchString(artifact.Version) ||
			!platformPattern.MatchString(artifact.OS) || !platformPattern.MatchString(artifact.Arch) ||
			!validDigest(artifact.SHA256) || artifact.URL == "" {
			return Catalog{}, errors.New("integration catalog contains an invalid artifact")
		}
		key := artifact.Name + "\x00" + artifact.Version + "\x00" + artifact.OS + "\x00" + artifact.Arch
		if seen[key] {
			return Catalog{}, fmt.Errorf("integration catalog contains duplicate artifact %s %s %s/%s", artifact.Name, artifact.Version, artifact.OS, artifact.Arch)
		}
		seen[key] = true
	}
	return catalog, nil
}

// Install never overwrites an installed version. It verifies the downloaded
// bytes before exposing the versioned path to GATEWAY_INTEGRATION_PATHS.
func Install(ctx context.Context, request InstallRequest) (string, error) {
	if !namePattern.MatchString(request.Name) || !versionPattern.MatchString(request.Version) {
		return "", errors.New("integration installation requires an exact name and vMAJOR.MINOR.PATCH version")
	}
	if !filepath.IsAbs(request.Dir) {
		return "", errors.New("integration installation directory must be absolute")
	}
	var catalog Catalog
	var err error
	if len(request.PublicKey) > 0 {
		catalog, err = ReadSigned(ctx, request.Catalog, request.PublicKey, request.Client)
	} else {
		catalog, err = Read(ctx, request.Catalog, request.Client)
	}
	if err != nil {
		return "", err
	}
	var chosen *Artifact
	for index := range catalog.Artifacts {
		artifact := &catalog.Artifacts[index]
		if artifact.Name == request.Name && artifact.Version == request.Version && artifact.OS == runtime.GOOS && artifact.Arch == runtime.GOARCH {
			chosen = artifact
			break
		}
	}
	if chosen == nil {
		return "", fmt.Errorf("integration %s %s has no artifact for %s/%s", request.Name, request.Version, runtime.GOOS, runtime.GOARCH)
	}
	assetReference, err := resolveAsset(request.Catalog, chosen.URL)
	if err != nil {
		return "", err
	}
	parent := filepath.Join(request.Dir, request.Name)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", fmt.Errorf("create integration directory: %w", err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("integration installation directory must not be a symlink")
	}
	stage, err := os.MkdirTemp(parent, ".download-")
	if err != nil {
		return "", fmt.Errorf("stage integration download: %w", err)
	}
	defer os.RemoveAll(stage)
	binaryName := "gateway-" + request.Name
	stagedBinary := filepath.Join(stage, binaryName)
	if err := download(ctx, assetReference, request.Client, stagedBinary, chosen.SHA256); err != nil {
		return "", err
	}
	receipt := Receipt{Name: chosen.Name, Version: chosen.Version, OS: chosen.OS, Arch: chosen.Arch, SHA256: chosen.SHA256}
	metadata, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return "", err
	}
	metadata = append(metadata, '\n')
	if err := writeSyncedFile(filepath.Join(stage, "receipt.json"), metadata, 0o640); err != nil {
		return "", fmt.Errorf("write integration receipt: %w", err)
	}
	if err := syncDirectory(stage); err != nil {
		return "", err
	}
	versionDir := filepath.Join(parent, request.Version)
	if err := os.Mkdir(versionDir, 0o750); err != nil {
		return "", fmt.Errorf("reserve integration version directory: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(versionDir)
		}
	}()
	if err := os.Rename(stagedBinary, filepath.Join(versionDir, binaryName)); err != nil {
		return "", fmt.Errorf("install integration binary: %w", err)
	}
	if err := os.Rename(filepath.Join(stage, "receipt.json"), filepath.Join(versionDir, "receipt.json")); err != nil {
		return "", fmt.Errorf("install integration receipt: %w", err)
	}
	if err := syncDirectory(versionDir); err != nil {
		return "", err
	}
	if err := syncDirectory(parent); err != nil {
		return "", err
	}
	completed = true
	return filepath.Join(versionDir, binaryName), nil
}

// VerifyInstalled checks an installed binary against its saved receipt.
func VerifyInstalled(path string) (Receipt, error) {
	if !filepath.IsAbs(path) {
		return Receipt{}, errors.New("integration path must be absolute")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "receipt.json"))
	if err != nil {
		return Receipt{}, fmt.Errorf("read integration receipt: %w", err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil || !namePattern.MatchString(receipt.Name) || !versionPattern.MatchString(receipt.Version) || !validDigest(receipt.SHA256) {
		return Receipt{}, errors.New("invalid integration receipt")
	}
	if filepath.Base(path) != "gateway-"+receipt.Name || filepath.Base(filepath.Dir(path)) != receipt.Version {
		return Receipt{}, errors.New("integration path does not match its receipt")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return Receipt{}, errors.New("installed integration is not an executable regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Receipt{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxArtifactBytes+1)); err != nil {
		return Receipt{}, err
	}
	if info.Size() > maxArtifactBytes || hex.EncodeToString(hash.Sum(nil)) != receipt.SHA256 {
		return Receipt{}, errors.New("installed integration digest does not match its receipt")
	}
	return receipt, nil
}

func download(ctx context.Context, reference string, client *http.Client, path, digest string) error {
	reader, err := openSource(ctx, reference, client, maxArtifactBytes)
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, maxArtifactBytes+1))
	if copyErr == nil && (written == 0 || written > maxArtifactBytes) {
		copyErr = errors.New("integration artifact is empty or exceeds its size limit")
	}
	if copyErr == nil && hex.EncodeToString(hash.Sum(nil)) != digest {
		copyErr = errors.New("integration artifact SHA-256 does not match the catalog")
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	return os.Chmod(path, 0o750)
}

func openSource(ctx context.Context, reference string, client *http.Client, maxBytes int64) (io.ReadCloser, error) {
	if strings.Contains(reference, "://") {
		parsed, err := url.Parse(reference)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return nil, errors.New("remote integration sources require HTTPS without URL credentials")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, reference, nil)
		if err != nil {
			return nil, err
		}
		if client == nil {
			client = &http.Client{Timeout: 30 * time.Second}
		}
		copyClient := *client
		previousRedirect := copyClient.CheckRedirect
		copyClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" || request.URL.User != nil {
				return errors.New("integration download redirected away from HTTPS")
			}
			if previousRedirect != nil {
				return previousRedirect(request, via)
			}
			if len(via) >= 10 {
				return errors.New("too many integration download redirects")
			}
			return nil
		}
		response, err := copyClient.Do(request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK || response.ContentLength > maxBytes {
			response.Body.Close()
			return nil, fmt.Errorf("integration source returned HTTP %d or exceeded its size limit", response.StatusCode)
		}
		return response.Body, nil
	}
	return os.Open(reference)
}

func resolveAsset(catalogReference, artifactReference string) (string, error) {
	if strings.Contains(catalogReference, "://") {
		base, err := url.Parse(catalogReference)
		if err != nil {
			return "", err
		}
		asset, err := url.Parse(artifactReference)
		if err != nil {
			return "", err
		}
		resolved := base.ResolveReference(asset)
		if resolved.Scheme != "https" || resolved.Host == "" || resolved.User != nil {
			return "", errors.New("remote integration artifact requires HTTPS")
		}
		return resolved.String(), nil
	}
	if filepath.IsAbs(artifactReference) || strings.Contains(artifactReference, "://") {
		return "", errors.New("local integration catalog requires relative artifact paths")
	}
	base, err := filepath.Abs(filepath.Dir(catalogReference))
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(base, artifactReference)
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("local integration artifact escapes the catalog directory")
	}
	return resolved, nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func writeSyncedFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}
