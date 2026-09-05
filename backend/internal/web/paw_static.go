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
			request := c.Request.Clone(c.Request.Context())
			request.URL.Path = "/" + cleanPath
			applyPawStaticAssetCacheHeaders(c.Writer.Header(), cleanPath)
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
	file, err := distFS.Open("index.html")
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

	// The security middleware puts a per-request nonce in the context. Next's
	// static export emits inline bootstrap scripts without one, so add the
	// nonce to those scripts before returning the document.
	content = injectPawScriptNonce(content, middleware.GetNonceFromContext(c))
	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
	c.Abort()
}

// injectPawScriptNonce adds nonce to inline script start tags while preserving
// the exported HTML bytes and leaving external scripts untouched.
func injectPawScriptNonce(content []byte, nonce string) []byte {
	if nonce == "" || !bytes.Contains(content, []byte("<script")) {
		return content
	}

	tokenizer := html.NewTokenizer(bytes.NewReader(content))
	var output bytes.Buffer
	output.Grow(len(content) + bytes.Count(content, []byte("<script>"))*(len(nonce)+16))
	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		if tokenType == html.ErrorToken {
			if tokenizer.Err() == io.EOF {
				break
			}
			return content
		}

		if tokenType == html.StartTagToken {
			tagName, _ := tokenizer.TagName()
			if string(tagName) == "script" {
				hasNonce, hasSrc := false, false
				for {
					key, _, moreAttr := tokenizer.TagAttr()
					switch string(key) {
					case "nonce":
						hasNonce = true
					case "src":
						hasSrc = true
					}
					if !moreAttr {
						break
					}
				}
				if !hasNonce && !hasSrc {
					if end := bytes.LastIndexByte(raw, '>'); end >= 0 {
						updated := make([]byte, 0, len(raw)+len(nonce)+16)
						updated = append(updated, raw[:end]...)
						updated = append(updated, ` nonce="`...)
						updated = append(updated, nonce...)
						updated = append(updated, `">`...)
						raw = updated
					}
				}
			}
		}
		output.Write(raw)
	}
	return output.Bytes()
}
