CREATE TABLE IF NOT EXISTS diagnosis_feedback (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id BIGINT UNSIGNED NOT NULL,
  rating VARCHAR(32) NOT NULL,
  corrected_root_cause TEXT,
  helpful_evidence_refs LONGTEXT,
  helpful_tool_names LONGTEXT,
  helpful_hypothesis_types LONGTEXT,
  comment TEXT,
  created_by VARCHAR(128),
  created_at DATETIME(3) NULL,
  INDEX idx_diagnosis_feedback_task_id (task_id),
  INDEX idx_diagnosis_feedback_rating (rating),
  INDEX idx_diagnosis_feedback_created_by (created_by)
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  actor VARCHAR(128),
  action VARCHAR(128) NOT NULL,
  namespace VARCHAR(128),
  resource_kind VARCHAR(64),
  resource_name VARCHAR(255),
  task_id BIGINT UNSIGNED NULL,
  summary TEXT,
  metadata_json LONGTEXT,
  created_at DATETIME(3) NULL,
  INDEX idx_audit_logs_actor (actor),
  INDEX idx_audit_logs_action (action),
  INDEX idx_audit_logs_namespace (namespace),
  INDEX idx_audit_logs_resource_kind (resource_kind),
  INDEX idx_audit_logs_resource_name (resource_name),
  INDEX idx_audit_logs_task_id (task_id)
);

CREATE TABLE IF NOT EXISTS runbooks (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  fault_type VARCHAR(128) NOT NULL,
  title VARCHAR(255) NOT NULL,
  content LONGTEXT,
  hints_json LONGTEXT,
  version INT NOT NULL DEFAULT 1,
  created_by VARCHAR(128),
  updated_by VARCHAR(128),
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  INDEX idx_runbooks_fault_type (fault_type),
  INDEX idx_runbooks_created_by (created_by),
  INDEX idx_runbooks_updated_by (updated_by)
);

CREATE TABLE IF NOT EXISTS runbook_chunks (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  runbook_id BIGINT UNSIGNED NOT NULL,
  chunk_index INT NOT NULL,
  title VARCHAR(255),
  content LONGTEXT,
  vector_id VARCHAR(255),
  created_at DATETIME(3) NULL,
  INDEX idx_runbook_chunks_runbook_id (runbook_id),
  INDEX idx_runbook_chunks_vector_id (vector_id)
);

CREATE TABLE IF NOT EXISTS diagnosis_queue_dead_letters (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  stream VARCHAR(128) NOT NULL,
  message_id VARCHAR(128),
  task_id BIGINT UNSIGNED,
  payload_json LONGTEXT,
  error TEXT,
  attempts INT,
  created_at DATETIME(3) NULL,
  INDEX idx_diagnosis_queue_dead_letters_stream (stream),
  INDEX idx_diagnosis_queue_dead_letters_message_id (message_id),
  INDEX idx_diagnosis_queue_dead_letters_task_id (task_id)
);

CREATE TABLE IF NOT EXISTS llm_usage_records (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id BIGINT UNSIGNED,
  provider VARCHAR(64),
  model VARCHAR(128),
  prompt_tokens INT,
  completion_tokens INT,
  total_tokens INT,
  latency_ms BIGINT,
  estimated_cost DOUBLE,
  created_at DATETIME(3) NULL,
  INDEX idx_llm_usage_records_task_id (task_id),
  INDEX idx_llm_usage_records_provider (provider),
  INDEX idx_llm_usage_records_model (model)
);
