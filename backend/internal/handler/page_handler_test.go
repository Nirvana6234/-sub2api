package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestCleanPageImageRelativePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "single filename", in: "logo.png", want: "logo.png", ok: true},
		{name: "nested path", in: "images/logo.png", want: filepath.Join("images", "logo.png"), ok: true},
		{name: "dot prefix", in: "./logo.png", want: "logo.png", ok: true},
		{name: "url escaped slash", in: "images%2Flogo.png", want: filepath.Join("images", "logo.png"), ok: true},
		{name: "parent traversal", in: "../secret.png", ok: false},
		{name: "encoded parent traversal", in: "%2e%2e/secret.png", ok: false},
		{name: "backslash traversal", in: `images\secret.png`, ok: false},
		{name: "absolute path", in: "/etc/passwd", ok: false},
		{name: "encoded absolute path", in: "%2fetc/passwd", ok: false},
		{name: "encoded nul byte", in: "logo.png%00", ok: false},
		{name: "invalid escape", in: "logo.png%zz", ok: false},
		{name: "empty path", in: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cleanPageImageRelativePath(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePageImagePath(t *testing.T) {
	root := t.TempDir()
	pagesDir := filepath.Join(root, "pages")
	base := filepath.Join(pagesDir, "guide")
	if err := os.MkdirAll(filepath.Join(base, "images"), 0755); err != nil {
		t.Fatalf("create images dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "logo.png"), []byte("fake"), 0644); err != nil {
		t.Fatalf("create direct image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "images", "logo.png"), []byte("fake"), 0644); err != nil {
		t.Fatalf("create image: %v", err)
	}

	got, ok := resolvePageImagePath(pagesDir, base, "logo.png")
	if !ok {
		t.Fatal("expected direct image path to be accepted")
	}
	want := mustEvalSymlinks(t, filepath.Join(base, "logo.png"))
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	got, ok = resolvePageImagePath(pagesDir, base, "images/logo.png")
	if !ok {
		t.Fatal("expected nested image path to be accepted")
	}
	want = mustEvalSymlinks(t, filepath.Join(base, "images", "logo.png"))
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	if got, ok := resolvePageImagePath(pagesDir, base, "../guide.md"); ok {
		t.Fatalf("expected traversal to be rejected, got %q", got)
	}
}

func TestResolvePageImagePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	pagesDir := filepath.Join(root, "pages")
	base := filepath.Join(pagesDir, "guide")
	outside := filepath.Join(root, "outside")

	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create page dir: %v", err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatalf("create outside dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("secret"), 0644); err != nil {
		t.Fatalf("create outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "images")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	if got, ok := resolvePageImagePath(pagesDir, base, "images/secret.png"); ok {
		t.Fatalf("expected symlink escape to be rejected, got %q", got)
	}
}

func TestPageImageRouteRequiresAuthenticationAndPageVisibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	pagesDir := filepath.Join(root, "pages")
	if err := os.MkdirAll(filepath.Join(pagesDir, "guide"), 0755); err != nil {
		t.Fatalf("create page directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagesDir, "guide.md"), []byte("# Guide"), 0644); err != nil {
		t.Fatalf("create page markdown: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagesDir, "guide", "logo.png"), []byte("\x89PNG\r\n\x1a\n"), 0644); err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagesDir, "guide", "secrets.json"), []byte(`{"secret":"value"}`), 0644); err != nil {
		t.Fatalf("create arbitrary file: %v", err)
	}

	settings := &pageHandlerSettingRepo{
		value: `[{"url":"md:guide","visibility":"user"}]`,
	}
	settingService := service.NewSettingService(settings, &config.Config{})
	router := gin.New()
	jwtAuth := func(c *gin.Context) {
		switch c.GetHeader("Authorization") {
		case "Bearer user-token":
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1})
			c.Set(string(middleware2.ContextKeyUserRole), "user")
			c.Next()
		case "Bearer admin-token":
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 2})
			c.Set(string(middleware2.ContextKeyUserRole), "admin")
			c.Next()
		default:
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	}
	adminAuth := func(c *gin.Context) {
		c.Next()
	}
	RegisterPageRoutes(router.Group("/api/v1"), root, jwtAuth, adminAuth, settingService)

	t.Run("unauthenticated image request is rejected", func(t *testing.T) {
		resp := performPageRequest(router, http.MethodGet, "/api/v1/pages/guide/images/logo.png", "")
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.Code, http.StatusUnauthorized)
		}
	})

	t.Run("ordinary user can fetch visible image", func(t *testing.T) {
		resp := performPageRequest(router, http.MethodGet, "/api/v1/pages/guide/images/logo.png", "Bearer user-token")
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
		}
		if got := resp.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Fatalf("cache-control = %q, want private, no-store", got)
		}
		if !strings.HasPrefix(resp.Header().Get("Content-Type"), "image/png") {
			t.Fatalf("content-type = %q, want image/png", resp.Header().Get("Content-Type"))
		}
	})

	t.Run("arbitrary file is not served by image route", func(t *testing.T) {
		resp := performPageRequest(router, http.MethodGet, "/api/v1/pages/guide/images/secrets.json", "Bearer user-token")
		if resp.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", resp.Code, http.StatusNotFound)
		}
	})

	t.Run("admin-only page is hidden from ordinary users", func(t *testing.T) {
		settings.value = `[{"url":"md:guide","visibility":"admin"}]`
		resp := performPageRequest(router, http.MethodGet, "/api/v1/pages/guide/images/logo.png", "Bearer user-token")
		if resp.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", resp.Code, http.StatusNotFound)
		}
	})

	t.Run("admin can fetch admin-only page image", func(t *testing.T) {
		resp := performPageRequest(router, http.MethodGet, "/api/v1/pages/guide/images/logo.png", "Bearer admin-token")
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
		}
	})

	t.Run("page content is private and uncached", func(t *testing.T) {
		settings.value = `[{"url":"md:guide","visibility":"user"}]`
		resp := performPageRequest(router, http.MethodGet, "/api/v1/pages/guide", "Bearer user-token")
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
		}
		if got := resp.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Fatalf("cache-control = %q, want private, no-store", got)
		}
	})
}

func performPageRequest(router http.Handler, method, path, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

type pageHandlerSettingRepo struct {
	value string
}

func (r *pageHandlerSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, errors.New("not implemented")
}

func (r *pageHandlerSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}

func (r *pageHandlerSettingRepo) Set(context.Context, string, string) error {
	return nil
}

func (r *pageHandlerSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *pageHandlerSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (r *pageHandlerSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *pageHandlerSettingRepo) Delete(context.Context, string) error {
	return nil
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()

	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks for %q: %v", path, err)
	}
	return realPath
}
