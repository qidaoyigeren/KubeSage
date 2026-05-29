package db

import (
	"fmt"
	"time"

	"kubesage/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// NewMySQL opens a GORM MySQL connection using the configured DSN options.
func NewMySQL(cfg config.MySQLConfig) (*gorm.DB, error) {
	parseTime := "False"
	if cfg.ParseTime {
		parseTime = "True"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%s&loc=%s",
		cfg.Username,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Database,
		cfg.Charset,
		parseTime,
		cfg.Loc,
	)

	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := database.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return database, nil
}
