package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// bearerAuth protects API routes when server.auth_token is configured.
func bearerAuth(token string) gin.HandlerFunc {
	token = strings.TrimSpace(token)
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		auth := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if got != token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		c.Next()
	}
}
