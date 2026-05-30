package db

import (
	"strings"
	"testing"

	"kubesage/internal/config"
)

func TestDSNUsesExplicitDSN(t *testing.T) {
	got := DSN(config.MySQLConfig{DSN: "user:pass@tcp(db:3306)/kubesage"})
	if got != "user:pass@tcp(db:3306)/kubesage" {
		t.Fatalf("unexpected dsn: %s", got)
	}
}

func TestDSNRendersStructuredConfig(t *testing.T) {
	got := DSN(config.MySQLConfig{
		Host: "127.0.0.1", Port: 3306, Username: "u", Password: "p", Database: "d", Charset: "utf8mb4", ParseTime: true, Loc: "Local",
	})
	for _, part := range []string{"u:p@tcp(127.0.0.1:3306)/d", "charset=utf8mb4", "parseTime=True"} {
		if !strings.Contains(got, part) {
			t.Fatalf("dsn %q missing %q", got, part)
		}
	}
}
