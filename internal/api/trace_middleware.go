package api

import (
	"kubesage/internal/observability"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// traceMiddleware creates an OpenTelemetry span for each HTTP request.
func traceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, span := observability.Tracer().Start(c.Request.Context(), "http "+c.Request.Method+" "+c.FullPath())
		defer span.End()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", c.FullPath()),
			attribute.Int("http.status_code", c.Writer.Status()),
		)
		if len(c.Errors) > 0 {
			span.RecordError(c.Errors.Last())
			span.SetStatus(codes.Error, c.Errors.Last().Error())
		}
		if c.Writer.Status() >= 500 {
			span.SetStatus(codes.Error, "server error")
		}
	}
}
