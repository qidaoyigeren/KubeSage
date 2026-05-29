package config

import (
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	MySQL      MySQLConfig      `mapstructure:"mysql"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`
	Diagnosis  DiagnosisConfig  `mapstructure:"diagnosis"`
	Prometheus PrometheusConfig `mapstructure:"prometheus"`
	Loki       LokiConfig       `mapstructure:"loki"`
	Runbook    RunbookConfig    `mapstructure:"runbook"`
	Log        LogConfig        `mapstructure:"log"`
}

type ServerConfig struct {
	Host                string `mapstructure:"host"`
	Port                int    `mapstructure:"port"`
	ReadTimeoutSeconds  int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `mapstructure:"write_timeout_seconds"`
}

type MySQLConfig struct {
	Host      string `mapstructure:"host"`
	Port      int    `mapstructure:"port"`
	Username  string `mapstructure:"username"`
	Password  string `mapstructure:"password"`
	Database  string `mapstructure:"database"`
	Charset   string `mapstructure:"charset"`
	ParseTime bool   `mapstructure:"parse_time"`
	Loc       string `mapstructure:"loc"`
}

type KubernetesConfig struct {
	Kubeconfig            string `mapstructure:"kubeconfig"`
	RequestTimeoutSeconds int    `mapstructure:"request_timeout_seconds"`
	DefaultLogTailLines   int64  `mapstructure:"default_log_tail_lines"`
}

type DiagnosisConfig struct {
	TaskTimeoutSeconds int `mapstructure:"task_timeout_seconds"`
}

type PrometheusConfig struct {
	BaseURL        string `mapstructure:"base_url"`
	Address        string `mapstructure:"address"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

type LokiConfig struct {
	Address        string `mapstructure:"address"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

type RunbookConfig struct {
	Dir string `mapstructure:"dir"`
}

type LogConfig struct {
	Level       string `mapstructure:"level"`
	Development bool   `mapstructure:"development"`
}

// Load reads configuration from a YAML file and environment variable overrides.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("KUBESAGE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// setDefaults defines safe defaults before Viper reads file/env values.
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout_seconds", 15)
	v.SetDefault("server.write_timeout_seconds", 30)
	v.SetDefault("kubernetes.request_timeout_seconds", 15)
	v.SetDefault("kubernetes.default_log_tail_lines", 200)
	v.SetDefault("diagnosis.task_timeout_seconds", 60)
	v.SetDefault("prometheus.timeout_seconds", 10)
	v.SetDefault("runbook.dir", "./runbooks")
	v.SetDefault("log.level", "info")
}
