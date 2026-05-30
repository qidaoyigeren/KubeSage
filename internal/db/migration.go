package db

import (
	"errors"
	"path/filepath"

	"kubesage/internal/config"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations applies versioned SQL migrations from the configured directory.
func RunMigrations(cfg config.MySQLConfig, dir string) error {
	if dir == "" {
		dir = "./migrations"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	m, err := migrate.New("file://"+filepath.ToSlash(abs), "mysql://"+DSN(cfg))
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
