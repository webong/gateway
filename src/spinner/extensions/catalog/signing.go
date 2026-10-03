package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const maxKeyFileBytes int64 = 256

// GenerateSigningKeys creates an Ed25519 key pair for an operator-owned
// release publisher. The private key must never be distributed with a bundle.
func GenerateSigningKeys(privatePath, publicPath string) error {
	if privatePath == "" || publicPath == "" || privatePath == publicPath {
		return errors.New("distinct private and public key paths are required")
	}
	if _, err := os.Lstat(publicPath); err == nil {
		return fmt.Errorf("public key path already exists: %s", publicPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := writeSyncedFile(privatePath, []byte(base64.StdEncoding.EncodeToString(private)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write private signing key: %w", err)
	}
	if err := writeSyncedFile(publicPath, []byte(base64.StdEncoding.EncodeToString(public)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write public signing key: %w; private key remains at %s", err, privatePath)
	}
	if err := syncDirectory(filepath.Dir(privatePath)); err != nil {
		return err
	}
	if filepath.Dir(publicPath) != filepath.Dir(privatePath) {
		if err := syncDirectory(filepath.Dir(publicPath)); err != nil {
			return err
		}
	}
	return nil
}

func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := readKeyFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

// SignCatalog writes a detached signature over the exact catalog.json bytes.
// The existing catalog and any existing signature are never overwritten.
func SignCatalog(path, privateKeyPath string) (string, error) {
	if strings.Contains(path, "://") {
		return "", errors.New("catalog signing requires a local file")
	}
	keyData, err := readKeyFile(privateKeyPath)
	if err != nil {
		return "", err
	}
	private, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyData)))
	if err != nil || len(private) != ed25519.PrivateKeySize {
		return "", errors.New("invalid Ed25519 private key")
	}
	contents, err := readSourceBytes(context.Background(), path, nil, maxCatalogBytes)
	if err != nil {
		return "", err
	}
	if _, err := decodeCatalog(contents); err != nil {
		return "", err
	}
	signaturePath := path + ".sig"
	signature := ed25519.Sign(ed25519.PrivateKey(private), contents)
	if err := writeSyncedFile(signaturePath, []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write extension catalog signature: %w", err)
	}
	if err := syncDirectory(filepath.Dir(signaturePath)); err != nil {
		return "", err
	}
	return signaturePath, nil
}

// ReadSigned verifies a detached catalog.json.sig with a pinned public key
// before interpreting any catalog entries. HTTPS remains mandatory remotely.
func ReadSigned(ctx context.Context, reference string, publicKey ed25519.PublicKey, client *http.Client) (Catalog, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return Catalog{}, errors.New("an Ed25519 public key is required")
	}
	contents, err := readSourceBytes(ctx, reference, client, maxCatalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	signatureReference, err := detachedSignatureReference(reference)
	if err != nil {
		return Catalog{}, err
	}
	signatureData, err := readSourceBytes(ctx, signatureReference, client, maxKeyFileBytes)
	if err != nil {
		return Catalog{}, fmt.Errorf("read extension catalog signature: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureData)))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, contents, signature) {
		return Catalog{}, errors.New("extension catalog signature verification failed")
	}
	return decodeCatalog(contents)
}

func detachedSignatureReference(reference string) (string, error) {
	if !strings.Contains(reference, "://") {
		return reference + ".sig", nil
	}
	parsed, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	parsed.Path += ".sig"
	parsed.RawPath = ""
	return parsed.String(), nil
}

func readKeyFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > int(maxKeyFileBytes) {
		return nil, errors.New("extension signing key exceeds its size limit")
	}
	return data, nil
}
