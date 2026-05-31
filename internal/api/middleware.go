package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"kubesage/internal/config"
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
	RoleAdmin    = "admin"
)

// bearerAuth protects API routes when bearer tokens are configured.
// In legacy bearer mode, the single server token is granted operator role.
func bearerAuth(token string, authCfgs ...config.AuthConfig) gin.HandlerFunc {
	token = strings.TrimSpace(token)
	authCfg := config.AuthConfig{DefaultRole: RoleOperator}
	if len(authCfgs) > 0 {
		authCfg = authCfgs[0]
	}
	hasConfiguredTokens := token != "" || hasRoleTokens(authCfg)
	return func(c *gin.Context) {
		if !hasConfiguredTokens {
			c.Set(contextActorKey, "anonymous")
			SetActorRole(c, defaultRole(authCfg))
			c.Next()
			return
		}
		auth := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if got != token && !containsToken(authCfg.AdminTokens, got) && !containsToken(authCfg.OperatorTokens, got) && !containsToken(authCfg.ViewerTokens, got) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		c.Set(contextActorKey, "bearer-user")
		c.Set(contextTokenKey, got)
		SetActorRole(c, roleForBearerToken(got, authCfg, RoleOperator))
		c.Next()
	}
}

func authMiddleware(mode, token string, client *k8s.Client, authCfgs ...config.AuthConfig) gin.HandlerFunc {
	authCfg := config.AuthConfig{Mode: mode, DefaultRole: RoleOperator}
	if len(authCfgs) > 0 {
		authCfg = authCfgs[0]
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "bearer" {
		return bearerAuth(token, authCfg)
	}
	if mode != "kubernetes_tokenreview" {
		return bearerAuth(token, authCfg)
	}
	if client == nil {
		return bearerAuth(token, authCfg)
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
		SetActorRole(c, defaultRole(authCfg))
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

// RequireOperator restricts access to operator-level users.
func RequireOperator() gin.HandlerFunc {
	return RequireRole(RoleOperator)
}

// RequireAdmin restricts access to admin-level users.
func RequireAdmin() gin.HandlerFunc {
	return RequireRole(RoleAdmin)
}

func RequireRole(required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := ActorRole(c)
		if roleRank(role) < roleRank(required) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": required + " role required"})
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

func roleForBearerToken(token string, cfg config.AuthConfig, fallback string) string {
	switch {
	case containsToken(cfg.AdminTokens, token):
		return RoleAdmin
	case containsToken(cfg.OperatorTokens, token):
		return RoleOperator
	case containsToken(cfg.ViewerTokens, token):
		return RoleViewer
	default:
		if fallback != "" {
			return fallback
		}
		return defaultRole(cfg)
	}
}

func defaultRole(cfg config.AuthConfig) string {
	switch strings.ToLower(strings.TrimSpace(cfg.DefaultRole)) {
	case RoleViewer:
		return RoleViewer
	case RoleAdmin:
		return RoleAdmin
	default:
		return RoleOperator
	}
}

func containsToken(tokens []string, token string) bool {
	for _, candidate := range tokens {
		if strings.TrimSpace(candidate) != "" && strings.TrimSpace(candidate) == token {
			return true
		}
	}
	return false
}

func hasRoleTokens(cfg config.AuthConfig) bool {
	return len(cfg.AdminTokens) > 0 || len(cfg.OperatorTokens) > 0 || len(cfg.ViewerTokens) > 0
}

func roleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	default:
		return 1
	}
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
