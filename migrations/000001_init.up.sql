CREATE TABLE IF NOT EXISTS diagnosis_tasks (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  namespace VARCHAR(128) NOT NULL,
  pod_name VARCHAR(255) NOT NULL,
  status VARCHAR(32) NOT NULL,
  alert_name VARCHAR(128),
  alert_severity VARCHAR(64),
  fault_type VARCHAR(64),
  root_cause_summary TEXT,
  confidence_score DOUBLE,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  finished_at DATETIME(3) NULL,
  INDEX idx_diagnosis_tasks_namespace (namespace),
  INDEX idx_diagnosis_tasks_pod_name (pod_name),
  INDEX idx_diagnosis_tasks_status (status),
  INDEX idx_diagnosis_tasks_alert_name (alert_name),
  INDEX idx_diagnosis_tasks_alert_severity (alert_severity),
  INDEX idx_diagnosis_tasks_fault_type (fault_type)
);

CREATE TABLE IF NOT EXISTS evidences (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id BIGINT UNSIGNED NOT NULL,
  source_type VARCHAR(64) NOT NULL,
  title VARCHAR(255) NOT NULL,
  content LONGTEXT,
  severity VARCHAR(32),
  raw_json LONGTEXT,
  created_at DATETIME(3) NULL,
  INDEX idx_evidences_task_id (task_id),
  INDEX idx_evidences_source_type (source_type),
  INDEX idx_evidences_severity (severity)
);

CREATE TABLE IF NOT EXISTS diagnosis_reports (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id BIGINT UNSIGNED NOT NULL,
  namespace VARCHAR(128) NOT NULL,
  pod_name VARCHAR(255) NOT NULL,
  fault_type VARCHAR(64),
  root_cause_summary TEXT,
  confidence_score DOUBLE,
  impact_analysis TEXT,
  suggested_actions LONGTEXT,
  remediation_actions LONGTEXT,
  risk_level VARCHAR(32),
  need_human_confirm BOOLEAN,
  rule_based_result LONGTEXT,
  llm_enhanced_summary LONGTEXT,
  created_at DATETIME(3) NULL,
  UNIQUE INDEX idx_diagnosis_reports_task_id (task_id),
  INDEX idx_diagnosis_reports_namespace (namespace),
  INDEX idx_diagnosis_reports_pod_name (pod_name),
  INDEX idx_diagnosis_reports_fault_type (fault_type)
);
