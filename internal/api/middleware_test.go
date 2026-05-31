package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"kubesage/internal/config"

	"github.com/gin-gonic/gin"
)

// TestBearerAuth verifies protected API routes require the configured token.
func TestBearerAuth(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(bearerAuth("secret"))
	router.GET("/protected", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 with token, got %d", rec.Code)
	}
}

func TestBearerAuthRoleTokens(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(bearerAuth("", config.AuthConfig{
		ViewerTokens:   []string{"viewer-token"},
		OperatorTokens: []string{"operator-token"},
		AdminTokens:    []string{"admin-token"},
	}))
	router.GET("/admin", RequireAdmin(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/operator", RequireOperator(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/operator", nil)
	req.Header.Set("Authorization", "Bearer viewer-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer on operator route, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/operator", nil)
	req.Header.Set("Authorization", "Bearer operator-token")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for operator route, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for admin route, got %d", rec.Code)
	}
}
