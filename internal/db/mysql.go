package db

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// NewMySQL opens a GORM MySQL connection using the configured DSN options.
func NewMySQL(cfg config.MySQLConfig) (*gorm.DB, error) {
	dsn := DSN(cfg)
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if len(cfg.ReadReplicas) > 0 {
		replicas := make([]gorm.Dialector, 0, len(cfg.ReadReplicas))
		for _, replica := range cfg.ReadReplicas {
			replica = strings.TrimSpace(replica)
			if replica == "" {
				continue
			}
			replicas = append(replicas, mysql.Open(replica))
		}
		if len(replicas) > 0 {
			if err := database.Use(dbresolver.Register(dbresolver.Config{Replicas: replicas}).SetConnMaxIdleTime(time.Minute)); err != nil {
				return nil, err
			}
		}
	}
	sqlDB, err := database.DB()
	if err != nil {
		return nil, err
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 10
	}
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 50
	}
	lifetime := time.Duration(cfg.ConnMaxLifetimeSec) * time.Second
	if lifetime <= 0 {
		lifetime = time.Hour
	}
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetConnMaxLifetime(lifetime)
	return database, nil
}

// DSN renders the MySQL connection string used by GORM and migrations.
func DSN(cfg config.MySQLConfig) string {
	if strings.TrimSpace(cfg.DSN) != "" {
		return strings.TrimSpace(cfg.DSN)
	}
	parseTime := "False"
	if cfg.ParseTime {
		parseTime = "True"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%s&loc=%s",
		cfg.Username,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Database,
		cfg.Charset,
		parseTime,
		cfg.Loc,
	)
}
