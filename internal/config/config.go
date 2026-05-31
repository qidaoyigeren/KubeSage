package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server       ServerConfig       `mapstructure:"server"`
	Auth         AuthConfig         `mapstructure:"auth"`
	MySQL        MySQLConfig        `mapstructure:"mysql"`
	Kubernetes   KubernetesConfig   `mapstructure:"kubernetes"`
	Diagnosis    DiagnosisConfig    `mapstructure:"diagnosis"`
	Agent        AgentConfig        `mapstructure:"agent"`
	Planner      PlannerConfig      `mapstructure:"planner"`
	Queue        QueueConfig        `mapstructure:"queue"`
	Retention    RetentionConfig    `mapstructure:"retention"`
	Prometheus   PrometheusConfig   `mapstructure:"prometheus"`
	LLM          LLMConfig          `mapstructure:"llm"`
	MCP          MCPConfig          `mapstructure:"mcp"`
	Loki         LokiConfig         `mapstructure:"loki"`
	Redis        RedisConfig        `mapstructure:"redis"`
	OTel         OTelConfig         `mapstructure:"otel"`
	Migrations   MigrationsConfig   `mapstructure:"migrations"`
	Runbook      RunbookConfig      `mapstructure:"runbook"`
	RAG          RAGConfig          `mapstructure:"rag"`
	Notification NotificationConfig `mapstructure:"notification"`
	Log          LogConfig          `mapstructure:"log"`
}

type ServerConfig struct {
	Host                string `mapstructure:"host"`
	Port                int    `mapstructure:"port"`
	ReadTimeoutSeconds  int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `mapstructure:"write_timeout_seconds"`
	AuthToken           string `mapstructure:"auth_token"`
}

type AuthConfig struct {
	Mode              string   `mapstructure:"mode"`
	DefaultRole       string   `mapstructure:"default_role"`
	ViewerTokens      []string `mapstructure:"viewer_tokens"`
	OperatorTokens    []string `mapstructure:"operator_tokens"`
	AdminTokens       []string `mapstructure:"admin_tokens"`
	AllowedNamespaces []string `mapstructure:"allowed_namespaces"`
}

type MySQLConfig struct {
	Host               string   `mapstructure:"host"`
	Port               int      `mapstructure:"port"`
	Username           string   `mapstructure:"username"`
	Password           string   `mapstructure:"password"`
	Database           string   `mapstructure:"database"`
	Charset            string   `mapstructure:"charset"`
	ParseTime          bool     `mapstructure:"parse_time"`
	Loc                string   `mapstructure:"loc"`
	DSN                string   `mapstructure:"dsn"`
	ReadReplicas       []string `mapstructure:"read_replicas"`
	MaxIdleConns       int      `mapstructure:"max_idle_conns"`
	MaxOpenConns       int      `mapstructure:"max_open_conns"`
	ConnMaxLifetimeSec int      `mapstructure:"conn_max_lifetime_seconds"`
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
	Enabled              bool   `mapstructure:"enabled"`
	MaxSteps             int    `mapstructure:"max_steps"`
	ToolTimeoutSeconds   int    `mapstructure:"tool_timeout_seconds"`
	EnableDryRunPreview  bool   `mapstructure:"enable_dry_run_preview"`
	HypothesisConfigPath string `mapstructure:"hypothesis_config_path"`
}

type PlannerConfig struct {
	Type string `mapstructure:"type"`
}

type QueueConfig struct {
	Type                string `mapstructure:"type"`
	Stream              string `mapstructure:"stream"`
	Group               string `mapstructure:"group"`
	Consumer            string `mapstructure:"consumer"`
	MaxRetry            int    `mapstructure:"max_retry"`
	RetryDelaySeconds   int    `mapstructure:"retry_delay_seconds"`
	BlockingSeconds     int    `mapstructure:"blocking_seconds"`
	DrainTimeoutSeconds int    `mapstructure:"drain_timeout_seconds"`
	MaxStreamLen        int64  `mapstructure:"max_stream_len"`
}

type RetentionConfig struct {
	TaskDays int `mapstructure:"task_days"`
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
	Enabled               bool               `mapstructure:"enabled"`
	BaseURL               string             `mapstructure:"base_url"`
	APIKey                string             `mapstructure:"api_key"`
	Model                 string             `mapstructure:"model"`
	RetryMaxAttempts      int                `mapstructure:"retry_max_attempts"`
	RetryInitialBackoffMS int                `mapstructure:"retry_initial_backoff_ms"`
	RetryMaxBackoffMS     int                `mapstructure:"retry_max_backoff_ms"`
	Grounding             LLMGroundingConfig `mapstructure:"grounding"`
}

type LLMGroundingConfig struct {
	Enabled              bool    `mapstructure:"enabled"`
	SemanticMinOverlap   float64 `mapstructure:"semantic_min_overlap"`
	PassRiskThreshold    float64 `mapstructure:"pass_risk_threshold"`
	WarningRiskThreshold float64 `mapstructure:"warning_risk_threshold"`
	RejectRiskThreshold  float64 `mapstructure:"reject_risk_threshold"`
}

type MCPConfig struct {
	Enabled bool              `mapstructure:"enabled"`
	Servers []MCPServerConfig `mapstructure:"servers"`
}

type MCPServerConfig struct {
	Name           string   `mapstructure:"name"`
	Command        string   `mapstructure:"command"`
	Args           []string `mapstructure:"args"`
	Env            []string `mapstructure:"env"`
	TimeoutSeconds int      `mapstructure:"timeout_seconds"`
}

type LokiConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	BaseURL        string `mapstructure:"base_url"`
	Address        string `mapstructure:"address"`
	TenantID       string `mapstructure:"tenant_id"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

type RedisConfig struct {
	Enabled          bool     `mapstructure:"enabled"`
	Mode             string   `mapstructure:"mode"`
	Address          string   `mapstructure:"address"`
	Addresses        []string `mapstructure:"addresses"`
	Password         string   `mapstructure:"password"`
	Username         string   `mapstructure:"username"`
	DB               int      `mapstructure:"db"`
	KeyPrefix        string   `mapstructure:"key_prefix"`
	SentinelMaster   string   `mapstructure:"sentinel_master"`
	SentinelUsername string   `mapstructure:"sentinel_username"`
	SentinelPassword string   `mapstructure:"sentinel_password"`
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

type RAGConfig struct {
	VectorStore  string          `mapstructure:"vector_store"`
	Embedding    EmbeddingConfig `mapstructure:"embedding"`
	Qdrant       QdrantConfig    `mapstructure:"qdrant"`
	PreferVector bool            `mapstructure:"prefer_vector"`
}

type EmbeddingConfig struct {
	Provider  string `mapstructure:"provider"`
	BaseURL   string `mapstructure:"base_url"`
	APIKey    string `mapstructure:"api_key"`
	Model     string `mapstructure:"model"`
	Dimension int    `mapstructure:"dimension"`
}

type QdrantConfig struct {
	BaseURL    string `mapstructure:"base_url"`
	APIKey     string `mapstructure:"api_key"`
	Collection string `mapstructure:"collection"`
}

type NotificationConfig struct {
	SlackWebhookURL       string `mapstructure:"slack_webhook_url"`
	FeishuWebhookURL      string `mapstructure:"feishu_webhook_url"`
	DingTalkWebhookURL    string `mapstructure:"dingtalk_webhook_url"`
	AlertmanagerWriteback bool   `mapstructure:"alertmanager_writeback"`
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SecurityWarnings returns a list of security-related warnings for the current
// configuration. These are non-fatal issues that should be addressed before
// production deployment.
func (c Config) SecurityWarnings() []string {
	var warnings []string
	if strings.TrimSpace(c.Server.AuthToken) == "" {
		warnings = append(warnings, "server.auth_token is empty: API is unauthenticated. Set KUBESAGE_SERVER_AUTH_TOKEN env var.")
	}
	if strings.TrimSpace(c.MySQL.Password) == "" {
		warnings = append(warnings, "mysql.password is empty. Set KUBESAGE_MYSQL_PASSWORD env var.")
	}
	if c.LLM.Enabled && strings.TrimSpace(c.LLM.APIKey) == "" {
		warnings = append(warnings, "llm.api_key is empty but LLM is enabled. Set KUBESAGE_LLM_API_KEY env var.")
	}
	if c.Redis.Enabled && strings.TrimSpace(c.Redis.Password) == "" {
		warnings = append(warnings, "redis.password is empty but Redis is enabled. Set KUBESAGE_REDIS_PASSWORD env var.")
	}
	if strings.EqualFold(c.RAG.VectorStore, "qdrant") && strings.TrimSpace(c.RAG.Qdrant.APIKey) == "" {
		warnings = append(warnings, "rag.qdrant.api_key is empty but Qdrant is configured. Set KUBESAGE_RAG_QDRANT_API_KEY env var.")
	}
	if strings.EqualFold(c.RAG.VectorStore, "qdrant") && strings.TrimSpace(c.RAG.Embedding.APIKey) == "" {
		warnings = append(warnings, "rag.embedding.api_key is empty but Qdrant is configured. Set KUBESAGE_RAG_EMBEDDING_API_KEY env var.")
	}
	return warnings
}

func (c Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if c.MySQL.DSN == "" {
		if strings.TrimSpace(c.MySQL.Host) == "" {
			return fmt.Errorf("mysql.host is required when mysql.dsn is empty")
		}
		if c.MySQL.Port <= 0 || c.MySQL.Port > 65535 {
			return fmt.Errorf("mysql.port must be between 1 and 65535")
		}
		if strings.TrimSpace(c.MySQL.Username) == "" {
			return fmt.Errorf("mysql.username is required")
		}
		if strings.TrimSpace(c.MySQL.Database) == "" {
			return fmt.Errorf("mysql.database is required")
		}
	}
	if c.Agent.MaxSteps < 0 || c.Agent.ToolTimeoutSeconds < 0 {
		return fmt.Errorf("agent max_steps and tool_timeout_seconds cannot be negative")
	}
	if c.Diagnosis.TaskTimeoutSeconds < 0 {
		return fmt.Errorf("diagnosis.task_timeout_seconds cannot be negative")
	}
	switch strings.ToLower(strings.TrimSpace(c.Planner.Type)) {
	case "", "rule", "llm":
	default:
		return fmt.Errorf("planner.type must be rule or llm")
	}
	switch strings.ToLower(strings.TrimSpace(c.Auth.Mode)) {
	case "", "bearer", "kubernetes_tokenreview":
	default:
		return fmt.Errorf("auth.mode must be bearer or kubernetes_tokenreview")
	}
	switch strings.ToLower(strings.TrimSpace(c.Auth.DefaultRole)) {
	case "", "viewer", "operator", "admin":
	default:
		return fmt.Errorf("auth.default_role must be viewer, operator, or admin")
	}
	if c.Queue.Type != "" && !strings.EqualFold(c.Queue.Type, "redis_stream") {
		return fmt.Errorf("queue.type must be empty or redis_stream")
	}
	if c.Redis.Enabled {
		switch strings.ToLower(strings.TrimSpace(c.Redis.Mode)) {
		case "", "single", "sentinel", "cluster":
		default:
			return fmt.Errorf("redis.mode must be single, sentinel, or cluster")
		}
	}
	if c.RAG.VectorStore != "" && !strings.EqualFold(c.RAG.VectorStore, "qdrant") {
		return fmt.Errorf("rag.vector_store must be empty or qdrant")
	}
	// Cross-field validation: when LLM is enabled, require base_url and api_key.
	if c.LLM.Enabled {
		if strings.TrimSpace(c.LLM.BaseURL) == "" {
			return fmt.Errorf("llm.base_url is required when llm.enabled is true")
		}
		if strings.TrimSpace(c.LLM.APIKey) == "" {
			return fmt.Errorf("llm.api_key is required when llm.enabled is true")
		}
	}
	if c.LLM.Grounding.SemanticMinOverlap < 0 || c.LLM.Grounding.SemanticMinOverlap > 1 {
		return fmt.Errorf("llm.grounding.semantic_min_overlap must be between 0 and 1")
	}
	if c.LLM.Grounding.PassRiskThreshold < 0 || c.LLM.Grounding.PassRiskThreshold > 1 ||
		c.LLM.Grounding.WarningRiskThreshold < 0 || c.LLM.Grounding.WarningRiskThreshold > 1 ||
		c.LLM.Grounding.RejectRiskThreshold < 0 || c.LLM.Grounding.RejectRiskThreshold > 1 {
		return fmt.Errorf("llm.grounding risk thresholds must be between 0 and 1")
	}
	if c.LLM.Grounding.WarningRiskThreshold > c.LLM.Grounding.RejectRiskThreshold {
		return fmt.Errorf("llm.grounding.warning_risk_threshold cannot exceed reject_risk_threshold")
	}
	// When RAG uses qdrant, require qdrant base_url and embedding api_key.
	if strings.EqualFold(c.RAG.VectorStore, "qdrant") {
		if strings.TrimSpace(c.RAG.Qdrant.BaseURL) == "" {
			return fmt.Errorf("rag.qdrant.base_url is required when rag.vector_store is qdrant")
		}
		if strings.TrimSpace(c.RAG.Embedding.APIKey) == "" {
			return fmt.Errorf("rag.embedding.api_key is required when rag.vector_store is qdrant")
		}
	}
	// When Redis is enabled, require at least one address.
	if c.Redis.Enabled && strings.TrimSpace(c.Redis.Address) == "" && len(c.Redis.Addresses) == 0 {
		return fmt.Errorf("redis.address or redis.addresses is required when redis.enabled is true")
	}
	// Validate retention days.
	if c.Retention.TaskDays < 0 {
		return fmt.Errorf("retention.task_days cannot be negative")
	}
	return nil
}

// setDefaults defines safe defaults before Viper reads file/env values.
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout_seconds", 15)
	v.SetDefault("server.write_timeout_seconds", 30)
	v.SetDefault("auth.mode", "bearer")
	v.SetDefault("auth.default_role", "operator")
	v.SetDefault("mysql.max_idle_conns", 10)
	v.SetDefault("mysql.max_open_conns", 50)
	v.SetDefault("mysql.conn_max_lifetime_seconds", 3600)
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
	v.SetDefault("planner.type", "llm")
	v.SetDefault("queue.type", "")
	v.SetDefault("queue.stream", "kubesage:diagnosis")
	v.SetDefault("queue.group", "kubesage-workers")
	v.SetDefault("queue.consumer", "worker-1")
	v.SetDefault("queue.max_retry", 3)
	v.SetDefault("queue.retry_delay_seconds", 5)
	v.SetDefault("queue.blocking_seconds", 5)
	v.SetDefault("queue.drain_timeout_seconds", 30)
	v.SetDefault("retention.task_days", 30)
	v.SetDefault("prometheus.timeout_seconds", 10)
	v.SetDefault("prometheus.retry_max_attempts", 2)
	v.SetDefault("prometheus.retry_initial_backoff_ms", 100)
	v.SetDefault("prometheus.retry_max_backoff_ms", 1000)
	v.SetDefault("llm.enabled", false)
	v.SetDefault("llm.model", "deepseek-chat")
	v.SetDefault("llm.retry_max_attempts", 2)
	v.SetDefault("llm.retry_initial_backoff_ms", 200)
	v.SetDefault("llm.retry_max_backoff_ms", 2000)
	v.SetDefault("llm.grounding.enabled", true)
	v.SetDefault("llm.grounding.semantic_min_overlap", 0.3)
	v.SetDefault("llm.grounding.pass_risk_threshold", 0.2)
	v.SetDefault("llm.grounding.warning_risk_threshold", 0.3)
	v.SetDefault("llm.grounding.reject_risk_threshold", 0.4)
	v.SetDefault("mcp.enabled", false)
	v.SetDefault("loki.enabled", false)
	v.SetDefault("loki.timeout_seconds", 10)
	v.SetDefault("redis.enabled", false)
	v.SetDefault("redis.mode", "single")
	v.SetDefault("redis.address", "127.0.0.1:6379")
	v.SetDefault("redis.key_prefix", "kubesage")
	v.SetDefault("otel.enabled", false)
	v.SetDefault("otel.service_name", "kubesage")
	v.SetDefault("otel.exporter", "stdout")
	v.SetDefault("migrations.enabled", true)
	v.SetDefault("migrations.dir", "./migrations")
	v.SetDefault("runbook.dir", "./runbooks")
	v.SetDefault("rag.vector_store", "")
	v.SetDefault("rag.prefer_vector", false)
	v.SetDefault("rag.embedding.provider", "")
	v.SetDefault("rag.embedding.model", "")
	v.SetDefault("rag.qdrant.collection", "kubesage_runbooks")
	v.SetDefault("log.level", "info")
}
