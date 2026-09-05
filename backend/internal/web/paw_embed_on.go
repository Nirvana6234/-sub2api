//go:build embed

package web

import (
	"embed"
	"io/fs"

	"github.com/gin-gonic/gin"
)

//go:embed all:paw_dist
var pawFrontendFS embed.FS

func HasEmbeddedPawFrontend() bool {
	_, err := pawFrontendFS.ReadFile("paw_dist/index.html")
	return err == nil
}

func ServeEmbeddedPawFrontend() gin.HandlerFunc {
	distFS, err := fs.Sub(pawFrontendFS, "paw_dist")
	if err != nil {
		panic("failed to get Paw frontend subdirectory: " + err.Error())
	}
	return newPawStaticHandler(distFS)
}
