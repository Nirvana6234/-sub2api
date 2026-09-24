package web

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
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

func TestPawHTMLScriptsMatchResponseCSP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const document = `<!doctype html><html><body><script>window.__PAW_CONFIG__={};</script><script src="/paw/_next/app.js" async></script><script nonce="stale">self.__next_f.push([1,"boot"])</script></body></html>`
	engine := gin.New()
	engine.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true}, nil))
	engine.Use(newPawStaticHandler(fstestMapFS(map[string]string{
		"index.html":          document,
		"install.html":        document,
		"_next/static/app.js": "console.log('paw')",
	})))

	seen := map[string]bool{}
	for _, path := range []string{"/paw/", "/paw/?pair=123456", "/paw/chat", "/paw/index.html", "/paw/install.html"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			policy := response.Header().Get("Content-Security-Policy")
			for _, directive := range strings.Split(policy, ";") {
				if strings.HasPrefix(strings.TrimSpace(directive), "script-src ") {
					require.NotContains(t, directive, "'unsafe-inline'")
				}
			}
			scripts := 0
			var requestNonce string
			parser := html.NewTokenizer(bytes.NewReader(response.Body.Bytes()))
			for parser.Next() != html.ErrorToken {
				token := parser.Token()
				if token.Type != html.StartTagToken || token.Data != "script" {
					continue
				}
				scripts++
				var nonces []string
				for _, attr := range token.Attr {
					if attr.Key == "nonce" {
						nonces = append(nonces, attr.Val)
					}
				}
				require.Len(t, nonces, 1)
				require.NotEmpty(t, nonces[0])
				require.NotEqual(t, "stale", nonces[0])
				require.Contains(t, policy, "'nonce-"+nonces[0]+"'")
				if requestNonce != "" {
					require.Equal(t, requestNonce, nonces[0])
				}
				requestNonce = nonces[0]
			}
			require.Equal(t, 3, scripts)
			require.False(t, seen[requestNonce], "nonce must change between responses")
			seen[requestNonce] = true
			require.Contains(t, response.Body.String(), `self.__next_f.push([1,"boot"])`)
		})
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/paw/_next/static/app.js", nil))
	require.Equal(t, "console.log('paw')", response.Body.String())
	require.Equal(t, "public, max-age=31536000, immutable", response.Header().Get("Cache-Control"))
}

func TestPawHTMLNoncePreservesScriptText(t *testing.T) {
	const document = `<!doctype html><!-- <script>ignored</script> --><SCRIPT data-value="a&amp;b" nonce="old">const sample = '<script nonce="old">';</SCRIPT><p>hello &amp; bye</p>`
	result := string(pawHTMLWithNonce([]byte(document), "current"))
	require.Contains(t, result, `<!-- <script>ignored</script> -->`)
	require.Contains(t, result, `const sample = '<script nonce="old">';`)
	require.Contains(t, result, `data-value="a&amp;b" nonce="current"`)
	require.Contains(t, result, `<p>hello &amp; bye</p>`)
	require.Equal(t, document, string(pawHTMLWithNonce([]byte(document), "")))
}
