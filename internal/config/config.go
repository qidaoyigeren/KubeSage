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
	Agent      AgentConfig      `mapstructure:"agent"`
	Prometheus PrometheusConfig `mapstructure:"prometheus"`
	LLM        LLMConfig        `mapstructure:"llm"`
	Loki       LokiConfig       `mapstructure:"loki"`
	Redis      RedisConfig      `mapstructure:"redis"`
	OTel       OTelConfig       `mapstructure:"otel"`
	Migrations MigrationsConfig `mapstructure:"migrations"`
	Runbook    RunbookConfig    `mapstructure:"runbook"`
	Log        LogConfig        `mapstructure:"log"`
}

type ServerConfig struct {
	Host                string `mapstructure:"host"`
	Port                int    `mapstructure:"port"`
	ReadTimeoutSeconds  int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `mapstructure:"write_timeout_seconds"`
	AuthToken           string `mapstructure:"auth_token"`
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
	TaskTimeoutSeconds     int `mapstructure:"task_timeout_seconds"`
	LogWindowBeforeSeconds int `mapstructure:"log_window_before_seconds"`
	LogWindowAfterSeconds  int `mapstructure:"log_window_after_seconds"`
	PodLockTTLSeconds      int `mapstructure:"pod_lock_ttl_seconds"`
	RetryMaxAttempts       int `mapstructure:"retry_max_attempts"`
	RetryInitialBackoffMS  int `mapstructure:"retry_initial_backoff_ms"`
	RetryMaxBackoffMS      int `mapstructure:"retry_max_backoff_ms"`
}

type AgentConfig struct {
	Enabled             bool `mapstructure:"enabled"`
	MaxSteps            int  `mapstructure:"max_steps"`
	ToolTimeoutSeconds  int  `mapstructure:"tool_timeout_seconds"`
	EnableDryRunPreview bool `mapstructure:"enable_dry_run_preview"`
}

type PrometheusConfig struct {
	BaseURL               string `mapstructure:"base_url"`
	Address               string `mapstructure:"address"`
	TimeoutSeconds        int    `mapstructure:"timeout_seconds"`
	RetryMaxAttempts      int    `mapstructure:"retry_max_attempts"`
	RetryInitialBackoffMS int    `mapstructure:"retry_initial_backoff_ms"`
	RetryMaxBackoffMS     int    `mapstructure:"retry_max_backoff_ms"`
}

type LLMConfig struct {
	Enabled               bool   `mapstructure:"enabled"`
	BaseURL               string `mapstructure:"base_url"`
	APIKey                string `mapstructure:"api_key"`
	Model                 string `mapstructure:"model"`
	RetryMaxAttempts      int    `mapstructure:"retry_max_attempts"`
	RetryInitialBackoffMS int    `mapstructure:"retry_initial_backoff_ms"`
	RetryMaxBackoffMS     int    `mapstructure:"retry_max_backoff_ms"`
}

type LokiConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	BaseURL        string `mapstructure:"base_url"`
	Address        string `mapstructure:"address"`
	TenantID       string `mapstructure:"tenant_id"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

type RedisConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	Address   string `mapstructure:"address"`
	Password  string `mapstructure:"password"`
	DB        int    `mapstructure:"db"`
	KeyPrefix string `mapstructure:"key_prefix"`
}

type OTelConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	ServiceName string `mapstructure:"service_name"`
	Exporter    string `mapstructure:"exporter"`
	Endpoint    string `mapstructure:"endpoint"`
	Insecure    bool   `mapstructure:"insecure"`
}

type MigrationsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Dir     string `mapstructure:"dir"`
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
	v.SetDefault("diagnosis.log_window_before_seconds", 120)
	v.SetDefault("diagnosis.log_window_after_seconds", 60)
	v.SetDefault("diagnosis.pod_lock_ttl_seconds", 300)
	v.SetDefault("diagnosis.retry_max_attempts", 2)
	v.SetDefault("diagnosis.retry_initial_backoff_ms", 100)
	v.SetDefault("diagnosis.retry_max_backoff_ms", 1000)
	v.SetDefault("agent.enabled", true)
	v.SetDefault("agent.max_steps", 12)
	v.SetDefault("agent.tool_timeout_seconds", 10)
	v.SetDefault("agent.enable_dry_run_preview", true)
	v.SetDefault("prometheus.timeout_seconds", 10)
	v.SetDefault("prometheus.retry_max_attempts", 2)
	v.SetDefault("prometheus.retry_initial_backoff_ms", 100)
	v.SetDefault("prometheus.retry_max_backoff_ms", 1000)
	v.SetDefault("llm.enabled", false)
	v.SetDefault("llm.model", "deepseek-chat")
	v.SetDefault("llm.retry_max_attempts", 2)
	v.SetDefault("llm.retry_initial_backoff_ms", 200)
	v.SetDefault("llm.retry_max_backoff_ms", 2000)
	v.SetDefault("loki.enabled", false)
	v.SetDefault("loki.timeout_seconds", 10)
	v.SetDefault("redis.enabled", false)
	v.SetDefault("redis.address", "127.0.0.1:6379")
	v.SetDefault("redis.key_prefix", "kubesage")
	v.SetDefault("otel.enabled", false)
	v.SetDefault("otel.service_name", "kubesage")
	v.SetDefault("otel.exporter", "stdout")
	v.SetDefault("migrations.enabled", true)
	v.SetDefault("migrations.dir", "./migrations")
	v.SetDefault("runbook.dir", "./runbooks")
	v.SetDefault("log.level", "info")
}
