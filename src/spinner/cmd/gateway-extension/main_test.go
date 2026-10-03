package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSignedCatalogCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gateway-sample"), []byte("test executable"), 0o750); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "publisher.key")
	public := filepath.Join(root, "publisher.pub")
	reference := filepath.Join(root, "catalog.json")
	installed := filepath.Join(root, "installed")
	for _, args := range [][]string{
		{"catalog", "--dir", root, "--version", "v1.2.3"},
		{"keygen", "--private-key", private, "--public-key", public},
	} {
		if err := run(args); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
	}
	if err := run([]string{"list", "--catalog", reference, "--public-key", public}); err == nil {
		t.Fatal("unsigned catalog was accepted with a pinned key")
	}
	if err := run([]string{"sign", "--catalog", reference, "--private-key", private}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"list", "--catalog", reference, "--public-key", public},
		{"install", "--catalog", reference, "--name", "sample", "--version", "v1.2.3", "--dir", installed, "--public-key", public},
		{"verify", "--path", filepath.Join(installed, "sample", "v1.2.3", "gateway-sample")},
	} {
		if err := run(args); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
	}
}
