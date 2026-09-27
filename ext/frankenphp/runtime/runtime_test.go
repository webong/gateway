package runtime

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeServesFrontControllerWithoutASecondPHPListener(t *testing.T) {
	appDir := t.TempDir()
	publicDir := appDir
	code := `<?php echo ($_SERVER['REQUEST_URI'] ?? '').'|'.($_SERVER['HTTP_X_TEST_HEADER'] ?? 'missing');`
	if err := os.WriteFile(filepath.Join(publicDir, "index.php"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "config.php"), []byte("<?php echo 'private configuration';"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime, err := Start(appDir)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	request := httptest.NewRequest(http.MethodGet, "http://router.test/health?check=1", nil)
	request.Header.Set("X_Test_Header", "spoofed")
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if got := strings.TrimSpace(response.Body.String()); got != "/health?check=1|missing" {
		t.Fatalf("unexpected Planner response %q", got)
	}
	response = httptest.NewRecorder()
	runtime.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://router.test/config.php", nil))
	if got := strings.TrimSpace(response.Body.String()); got != "/config.php|missing" {
		t.Fatalf("request bypassed the flat front controller: %q", got)
	}
}
