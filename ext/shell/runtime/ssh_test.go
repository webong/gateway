package runtime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/webong/gateway/driver/automation"
	"golang.org/x/crypto/ssh"
)

func TestSSHShellReplyUsesPinnedForcedCommandAndBoundedInput(t *testing.T) {
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	clientBlock, err := ssh.MarshalPrivateKey(clientPrivate, "")
	if err != nil {
		t.Fatal(err)
	}
	clientKey := string(pem.EncodeToMemory(clientBlock))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	seen := make(chan string, 1)
	server := &ssh.ServerConfig{NoClientAuth: false, PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(clientSigner.PublicKey().Marshal()) {
			return nil, os.ErrPermission
		}
		return nil, nil
	}}
	server.AddHostKey(hostSigner)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, channels, _, err := ssh.NewServerConn(conn, server)
				if err != nil {
					return
				}
				for channel := range channels {
					if channel.ChannelType() != "session" {
						channel.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					stream, requests, err := channel.Accept()
					if err != nil {
						return
					}
					go func() {
						defer stream.Close()
						for request := range requests {
							if request.Type != "exec" || len(request.Payload) < 4 {
								request.Reply(false, nil)
								continue
							}
							command := string(request.Payload[4:])
							if int(binary.BigEndian.Uint32(request.Payload[:4])) != len(command) || command != "gateway-shell-helper" {
								request.Reply(false, nil)
								continue
							}
							request.Reply(true, nil)
							body, _ := io.ReadAll(io.LimitReader(stream, 65537))
							seen <- string(body)
							_, _ = stream.Write([]byte(`{"Response":{"status":202,"body":"accepted"}}`))
							stream.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
							return
						}
					}()
				}
			}()
		}
	}()
	root := t.TempDir()
	engine, err := New(Config{HooksDir: root, SSHAllowedCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	// Resolve through the same authenticated private HTTP contract as Planner.
	if err := engine.SetSSHResolverHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/_internal/gateway/shell/executions/2c034453-f6ef-4f98-a9a2-6dfb624758d0" ||
			request.Header.Get("X-Gateway-Internal") != "private-token" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(SSHExecution{Hook: "github/deploy", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
			User: "gateway", HostPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))),
			PrivateKey: clientKey})
	}), "private-token"); err != nil {
		t.Fatal(err)
	}
	outcome, err := automation.NewRuntime(engine).Execute(context.Background(), automation.Script{Language: Language,
		Source: "ssh:2c034453-f6ef-4f98-a9a2-6dfb624758d0"}, automation.Input{Event: map[string]any{"delivery_id": "delivery-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Response == nil || outcome.Response.Status != 202 || outcome.Response.Body != "accepted" || outcome.RunID == "" {
		t.Fatalf("unexpected SSH outcome: %#v", outcome)
	}
	var received map[string]any
	if err := json.Unmarshal([]byte(<-seen), &received); err != nil {
		t.Fatal(err)
	}
	if received["hook"] != "github/deploy" {
		t.Fatalf("remote helper received %#v", received)
	}
	_, wrongPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongSigner, err := ssh.NewSignerFromKey(wrongPrivate)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := engine.ContextWithSSHResolverHandler(context.Background(), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(SSHExecution{Hook: "github/deploy", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
			User: "gateway", HostPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(wrongSigner.PublicKey()))),
			PrivateKey: clientKey})
	}), "private-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Execute(scoped, "ssh:2c034453-f6ef-4f98-a9a2-6dfb624758d0", automation.Input{})
	if err == nil || !strings.Contains(err.Error(), "handshake failed") {
		t.Fatalf("mismatched host key was accepted: %v", err)
	}
}

func TestRemoteOnlyShellEngineCannotExecuteLocalPaths(t *testing.T) {
	engine, err := New(Config{SSHAllowedCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Execute(context.Background(), "github/deploy", automation.Input{})
	if err == nil || !strings.Contains(err.Error(), "local Shell hooks are not configured") {
		t.Fatalf("remote-only engine executed or accepted local path: %v", err)
	}
}
