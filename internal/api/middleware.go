package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"kubesage/internal/k8s"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	contextActorKey     = "actor"
	contextTokenKey     = "bearer_token"
	contextActorRoleKey = "actor_role"
)

const (
	RoleViewer   = "viewer"
	RoleOperator = "operator"
)

// bearerAuth protects API routes when server.auth_token is configured.
// In bearer mode, all authenticated users are granted operator role.
func bearerAuth(token string) gin.HandlerFunc {
	token = strings.TrimSpace(token)
	return func(c *gin.Context) {
		if token == "" {
			// No token configured — open access, grant operator role.
			c.Set(contextActorKey, "anonymous")
			SetActorRole(c, RoleOperator)
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
		c.Set(contextActorKey, "bearer-user")
		c.Set(contextTokenKey, got)
		SetActorRole(c, RoleOperator)
		c.Next()
	}
}

func authMiddleware(mode, token string, client *k8s.Client) gin.HandlerFunc {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "bearer" {
		return bearerAuth(token)
	}
	if mode != "kubernetes_tokenreview" {
		return bearerAuth(token)
	}
	if client == nil {
		return bearerAuth(token)
	}
	return func(c *gin.Context) {
		auth := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		user, ok, err := client.ReviewToken(ctx, got)
		if err != nil || !ok || user == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		c.Set(contextActorKey, user.Username)
		c.Set(contextTokenKey, got)
		// In kubernetes_tokenreview mode, grant operator role by default.
		// Fine-grained RBAC is enforced at the handler level via AuthorizePodDiagnosis.
		SetActorRole(c, RoleOperator)
		c.Next()
	}
}

func Actor(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Get(contextActorKey); ok {
		if actor, ok := value.(string); ok {
			return actor
		}
	}
	return "anonymous"
}

func BearerToken(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Get(contextTokenKey); ok {
		if token, ok := value.(string); ok {
			return token
		}
	}
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

// RequireOperator is a middleware that restricts access to operator-level users.
// In bearer mode, all authenticated users are treated as operators.
// In kubernetes_tokenreview mode, the role is determined by RBAC checks.
func RequireOperator() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := ActorRole(c)
		if role != RoleOperator {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "operator role required"})
			return
		}
		c.Next()
	}
}

// ActorRole returns the authenticated user's role or "viewer" as default.
func ActorRole(c *gin.Context) string {
	if c == nil {
		return RoleViewer
	}
	if value, ok := c.Get(contextActorRoleKey); ok {
		if role, ok := value.(string); ok {
			return role
		}
	}
	return RoleViewer
}

// SetActorRole stores the role in the gin context.
func SetActorRole(c *gin.Context, role string) {
	c.Set(contextActorRoleKey, role)
}

// AuditRecorder is a minimal interface for recording audit events.
type AuditRecorder interface {
	RecordAudit(ctx context.Context, actor, action, namespace, resourceKind, resourceName string, taskID *uint, summary string, metadata interface{}) error
}

// auditMiddleware logs every API request to the audit store.
func auditMiddleware(recorder AuditRecorder, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		actor := Actor(c)
		status := c.Writer.Status()

		// Skip health/metrics endpoints.
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/health") || strings.HasPrefix(path, "/readyz") ||
			strings.HasPrefix(path, "/metrics") {
			return
		}

		if recorder != nil {
			_ = recorder.RecordAudit(c.Request.Context(), actor,
				c.Request.Method+" "+path,
				"", "http", "",
				nil,
				http.StatusText(status),
				map[string]interface{}{
					"status":  status,
					"latency": latency.Milliseconds(),
					"method":  c.Request.Method,
					"path":    path,
				},
			)
		}
	}
}
