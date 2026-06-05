package config

import "testing"

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
