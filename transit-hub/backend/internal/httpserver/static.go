package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// staticHandler 提供前端静态文件服务和 Vue history 路由回退。
// API 请求（/api/）不经过此 handler，由调用方在路由层分流。
func staticHandler(publicDir string) http.Handler {
	fs := http.Dir(publicDir)
	fileServer := http.FileServer(fs)
	indexPath := filepath.Join(publicDir, "index.html")
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, indexPath)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 安全：禁止路径穿越
		cleanPath := filepath.Clean(r.URL.Path)
		if strings.Contains(cleanPath, "..") {
			http.NotFound(w, r)
			return
		}

		// 检查文件是否存在
		fullPath := filepath.Join(publicDir, filepath.FromSlash(cleanPath))
		info, err := os.Stat(fullPath)
		if err == nil && !info.IsDir() {
			// 文件存在，直接返回
			if cleanPath == string(filepath.Separator)+"index.html" {
				serveIndex(w, r)
				return
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// 文件不存在或是目录，回退到 index.html（Vue history 路由）
		if _, err := os.Stat(indexPath); err != nil {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r)
	})
}
