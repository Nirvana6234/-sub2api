package web

import (
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
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

	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
	c.Abort()
}
