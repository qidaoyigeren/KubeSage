package logger

import (
	"kubesage/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Init creates the process logger from the configured log level and mode.
func Init(cfg config.LogConfig) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if err := level.Set(cfg.Level); err != nil {
		level = zapcore.InfoLevel
	}

	zapCfg := zap.NewProductionConfig()
	if cfg.Development {
		zapCfg = zap.NewDevelopmentConfig()
	}
	zapCfg.Level = zap.NewAtomicLevelAt(level)
	return zapCfg.Build()
}
