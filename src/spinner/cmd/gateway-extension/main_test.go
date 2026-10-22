package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProbeValidatesExecutableAgainstServerSpecification(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	plugin := filepath.Join(t.TempDir(), "gateway-centrifugo")
	build := exec.Command("go", "build", "-mod=mod", "-o", plugin, "./cmd/plugin")
	build.Dir = filepath.Join(root, "ext", "centrifugo")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build extension: %v\n%s", err, output)
	}
	t.Setenv("GATEWAY_CENTRIFUGO_DOCKER_IMAGE", "centrifugo/centrifugo:v6")
	spec := filepath.Join(t.TempDir(), "server.json")
	writeSpec := func(configuration string) {
		t.Helper()
		payload := `{"assignment_id":"assignment-1","id":"server-1","type":"centrifugo","name":"events","hostname":"events.example.test","driver":"docker","desired_state":"running","replicas":1,"revision":1,"configuration":` + configuration + `}`
		if err := os.WriteFile(spec, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSpec(`{"client_token_hmac_secret_key":"secret","http_api_key":"api-secret","engine_type":"memory"}`)
	if err := run([]string{"probe", "--path", plugin, "--spec", spec, "--driver", "docker"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"probe", "--path", plugin, "--spec", spec, "--driver", "kubernetes"}); err == nil {
		t.Fatal("probe accepted a driver that differs from the server specification")
	}
	writeSpec(`{"client_token_hmac_secret_key":"secret","http_api_key":"api-secret","engine_type":"invalid"}`)
	if err := run([]string{"probe", "--path", plugin, "--spec", spec, "--driver", "docker"}); err == nil {
		t.Fatal("probe accepted an invalid workload configuration")
	}
}

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
