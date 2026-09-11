package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticHandlerIndexIsNotCached(t *testing.T) {
	publicDir := t.TempDir()
	mustWriteStaticFile(t, publicDir, "index.html", "<html>app</html>")

	handler := staticHandler(publicDir)
	tests := []struct {
		name        string
		requestPath string
		wantStatus  int
	}{
		{name: "root", requestPath: "/", wantStatus: http.StatusOK},
		{name: "direct index redirect", requestPath: "/index.html", wantStatus: http.StatusMovedPermanently},
		{name: "history fallback", requestPath: "/admin/purity-check", wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.requestPath, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-cache" {
				t.Fatalf("Cache-Control = %q, want %q", got, "no-cache")
			}
		})
	}
}

func TestStaticHandlerLeavesAssetCacheControlUnchanged(t *testing.T) {
	publicDir := t.TempDir()
	mustWriteStaticFile(t, publicDir, "index.html", "<html>app</html>")
	mustWriteStaticFile(t, publicDir, "assets/app-a1b2c3.js", "console.log('app')")

	request := httptest.NewRequest(http.MethodGet, "/assets/app-a1b2c3.js", nil)
	response := httptest.NewRecorder()
	staticHandler(publicDir).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("Cache-Control = %q, want existing empty value", got)
	}
}

func mustWriteStaticFile(t *testing.T, publicDir, name, contents string) {
	t.Helper()
	path := filepath.Join(publicDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
