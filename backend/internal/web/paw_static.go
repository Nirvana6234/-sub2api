package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
)

const pawFrontendPrefix = "/paw"

func newPawStaticHandler(distFS fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(distFS))

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path != pawFrontendPrefix && !strings.HasPrefix(path, pawFrontendPrefix+"/") {
			c.Next()
			return
		}

		cleanPath := strings.TrimPrefix(path, pawFrontendPrefix)
		cleanPath = strings.TrimPrefix(cleanPath, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		if cleanPath == "index.html" {
			servePawIndex(c, distFS)
			return
		}

		if pawFileExists(distFS, cleanPath) {
			if strings.HasSuffix(strings.ToLower(cleanPath), ".html") {
				servePawHTML(c, distFS, cleanPath)
				return
			}
			request := c.Request.Clone(c.Request.Context())
			request.URL.Path = "/" + cleanPath
			applyPawStaticAssetCacheHeaders(c.Writer.Header(), cleanPath)
			if strings.HasSuffix(strings.ToLower(cleanPath), ".js") {
				c.Writer.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			}
			fileServer.ServeHTTP(c.Writer, request)
			c.Abort()
			return
		}

		servePawIndex(c, distFS)
	}
}

func applyPawStaticAssetCacheHeaders(header http.Header, cleanPath string) {
	if header == nil || !strings.HasPrefix(strings.TrimPrefix(cleanPath, "/"), "_next/static/") {
		return
	}
	header.Set("Cache-Control", "public, max-age=31536000, immutable")
}

func pawFileExists(distFS fs.FS, path string) bool {
	file, err := distFS.Open(path)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func servePawIndex(c *gin.Context, distFS fs.FS) {
	servePawHTML(c, distFS, "index.html")
}

func servePawHTML(c *gin.Context, distFS fs.FS, path string) {
	file, err := distFS.Open(path)
	if err != nil {
		c.String(http.StatusNotFound, "Paw frontend not found")
		c.Abort()
		return
	}
	defer func() { _ = file.Close() }()

	content, err := io.ReadAll(file)
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to read Paw index.html")
		c.Abort()
		return
	}

	// Next's exported HTML includes inline configuration and hydration scripts.
	// Their nonce must match this response's SecurityHeaders middleware nonce.
	content = pawHTMLWithNonce(content, middleware.GetNonceFromContext(c))
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
	c.Abort()
}

// Only rewrite script start tags in trusted, embedded build output. Tokenizing
// preserves script bodies and avoids rewriting tag-like text inside scripts.
func pawHTMLWithNonce(content []byte, nonce string) []byte {
	if nonce == "" {
		return content
	}
	var output bytes.Buffer
	output.Grow(len(content))
	tokenizer := html.NewTokenizer(bytes.NewReader(content))
	for {
		kind := tokenizer.Next()
		raw := tokenizer.Raw()
		if kind == html.ErrorToken {
			output.Write(raw)
			return output.Bytes()
		}
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			name, _ := tokenizer.TagName()
			if bytes.Equal(name, []byte("script")) {
				// Parse this opening tag separately; TagName advances the tokenizer.
				tagParser := html.NewTokenizer(bytes.NewReader(raw))
				tagParser.Next()
				tag := tagParser.Token()
				attrs := tag.Attr[:0]
				for _, attr := range tag.Attr {
					if attr.Key != "nonce" {
						attrs = append(attrs, attr)
					}
				}
				tag.Attr = append(attrs, html.Attribute{Key: "nonce", Val: nonce})
				output.WriteString(tag.String())
				continue
			}
		}
		output.Write(raw)
	}
}
