package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPawStaticHandlerServesIndexAndAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	distFS := fstestMapFS(map[string]string{
		"index.html":           "<!doctype html><html><body>Paw</body></html>",
		"_next/app.js":         "console.log('paw')",
		"manifest.webmanifest": `{"name":"Paw"}`,
	})

	engine := gin.New()
	engine.Use(newPawStaticHandler(distFS))

	for _, test := range []struct {
		name        string
		path        string
		contentType string
		body        string
	}{
		{name: "root", path: "/paw", contentType: "text/html; charset=utf-8", body: "Paw"},
		{name: "slash root", path: "/paw/", contentType: "text/html; charset=utf-8", body: "Paw"},
		{name: "spa route", path: "/paw/chat", contentType: "text/html; charset=utf-8", body: "Paw"},
		{name: "asset", path: "/paw/_next/app.js", contentType: "application/javascript", body: "console.log"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			require.Contains(t, response.Header().Get("Content-Type"), test.contentType)
			require.Contains(t, response.Body.String(), test.body)
		})
	}
}

func TestPawStaticHandlerLeavesOtherRoutesAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	distFS := fstestMapFS(map[string]string{"index.html": "Paw"})
	engine := gin.New()
	engine.Use(newPawStaticHandler(distFS))
	engine.GET("/api/v1/paw/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	engine.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "root")
	})

	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/api/v1/paw/config", body: `{"ok":true}`},
		{path: "/", body: "root"},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), test.body)
	}
}

func fstestMapFS(files map[string]string) fs.FS {
	mapped := fstest.MapFS{}
	for path, content := range files {
		mapped[path] = &fstest.MapFile{Data: []byte(content)}
	}
	return mapped
}
