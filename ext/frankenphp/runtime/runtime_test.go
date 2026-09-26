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
	publicDir := filepath.Join(appDir, "public")
	if err := os.Mkdir(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	code := `<?php echo ($_SERVER['REQUEST_URI'] ?? '').'|'.($_SERVER['HTTP_X_TEST_HEADER'] ?? 'missing');`
	if err := os.WriteFile(filepath.Join(publicDir, "index.php"), []byte(code), 0o644); err != nil {
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
}
