package extensions

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestArtifactDownloadFollowsVerifiedHTTPSRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release" {
			http.Redirect(w, r, "/object", http.StatusFound)
			return
		}
		if r.URL.Path != "/object" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			t.Error("redirect leaked request credentials or referring URL")
		}
		_, _ = io.WriteString(w, "archive bytes")
	}))
	defer server.Close()
	client := NewArtifactHTTPClient()
	client.Transport = server.Client().Transport
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/release", nil)
	request.Header.Set("Authorization", "Bearer disposable-test-credential")
	request.Header.Set("Cookie", "fixture=private")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || string(body) != "archive bytes" {
		t.Fatalf("archive not downloaded after TLS redirect: %d %q", response.StatusCode, body)
	}
}

func TestArtifactDownloadRejectsDowngradeCredentialsLoopsAndUntrustedTLS(t *testing.T) {
	var insecureRequests atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { insecureRequests.Add(1) }))
	defer insecure.Close()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/object" {
			w.WriteHeader(http.StatusOK)
			return
		}
		target := map[string]string{"/downgrade": insecure.URL, "/credentials": strings.Replace(server.URL, "https://", "https://user:secret@", 1), "/loop": server.URL + "/loop"}[r.URL.Path]
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer server.Close()
	for _, path := range []string{"/downgrade", "/credentials", "/loop"} {
		t.Run(path, func(t *testing.T) {
			client := NewArtifactHTTPClient()
			client.Transport = server.Client().Transport
			response, err := client.Get(server.URL + path)
			if response != nil {
				response.Body.Close()
			}
			if err == nil {
				t.Fatalf("unsafe redirect accepted: %s", path)
			}
		})
	}
	if insecureRequests.Load() != 0 {
		t.Fatal("artifact downloaded over plaintext HTTP")
	}
	response, err := NewArtifactHTTPClient().Get(server.URL + "/object")
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("untrusted artifact TLS certificate was accepted")
	}
}
