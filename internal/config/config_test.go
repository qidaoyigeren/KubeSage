package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestValidateRejectsInvalidPlanner(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Port: 8080},
		MySQL:   MySQLConfig{Host: "127.0.0.1", Port: 3306, Username: "u", Database: "d"},
		Planner: PlannerConfig{Type: "magic"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected invalid planner error")
	}
}

func TestValidateAcceptsRedisStreamAndQdrant(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Port: 8080},
		MySQL:   MySQLConfig{Host: "127.0.0.1", Port: 3306, Username: "u", Database: "d"},
		Planner: PlannerConfig{Type: "llm"},
		Queue:   QueueConfig{Type: "redis_stream"},
		Redis:   RedisConfig{Enabled: true, Mode: "sentinel", Address: "127.0.0.1:6379"},
		RAG: RAGConfig{
			VectorStore: "qdrant",
			Qdrant:      QdrantConfig{BaseURL: "http://localhost:6333"},
			Embedding:   EmbeddingConfig{BaseURL: "https://api.example.com/v1", APIKey: "test-key", Model: "text-embedding-3-small"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateAcceptsPgvector(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Port: 8080},
		MySQL:   MySQLConfig{Host: "127.0.0.1", Port: 3306, Username: "u", Database: "d"},
		Planner: PlannerConfig{Type: "rule"},
		RAG: RAGConfig{
			VectorStore: "pgvector",
			PGVector:    PGVectorConfig{DSN: "postgres://kubesage:kubesage@localhost:5432/kubesage_vector?sslmode=disable"},
			Embedding:   EmbeddingConfig{BaseURL: "https://api.example.com/v1", APIKey: "test-key", Model: "text-embedding-3-small"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestAgentBudgetDefaults(t *testing.T) {
	v := viper.New()
	setDefaults(v)
	expected := map[string]int{
		"agent.max_reflection_steps":       4,
		"agent.max_reflection_rounds":      4,
		"agent.max_tool_calls_per_tool":    3,
		"agent.max_runbook_searches":       2,
		"agent.max_llm_tokens":             12000,
		"agent.reflection_timeout_seconds": 15,
	}
	for key, want := range expected {
		if got := v.GetInt(key); got != want {
			t.Fatalf("%s = %d, want %d", key, got, want)
		}
	}
}

func TestValidateRejectsNegativeAgentBudget(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Port: 8080},
		MySQL:   MySQLConfig{Host: "127.0.0.1", Port: 3306, Username: "u", Database: "d"},
		Planner: PlannerConfig{Type: "rule"},
		Agent:   AgentConfig{MaxReflectionSteps: -1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative agent budget validation error")
	}
}
