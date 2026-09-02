//go:build !embed

package web

import "github.com/gin-gonic/gin"

func HasEmbeddedPawFrontend() bool {
	return false
}

func ServeEmbeddedPawFrontend() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}
