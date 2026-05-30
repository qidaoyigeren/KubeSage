CREATE TABLE IF NOT EXISTS agent_steps (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id BIGINT UNSIGNED NOT NULL,
  parent_step_id BIGINT UNSIGNED NULL,
  trace_id VARCHAR(64),
  step_index INT NOT NULL,
  stage VARCHAR(32) NOT NULL,
  tool_name VARCHAR(128),
  input_json LONGTEXT,
  output_json LONGTEXT,
  status VARCHAR(32) NOT NULL,
  duration_ms BIGINT,
  reasoning_summary TEXT,
  created_at DATETIME(3) NULL,
  INDEX idx_agent_steps_task_id (task_id),
  INDEX idx_agent_steps_parent_step_id (parent_step_id),
  INDEX idx_agent_steps_trace_id (trace_id),
  INDEX idx_agent_steps_step_index (step_index),
  INDEX idx_agent_steps_stage (stage),
  INDEX idx_agent_steps_tool_name (tool_name),
  INDEX idx_agent_steps_status (status)
);

CREATE TABLE IF NOT EXISTS hypotheses (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id BIGINT UNSIGNED NOT NULL,
  hypothesis_type VARCHAR(128) NOT NULL,
  summary TEXT,
  confidence_score DOUBLE,
  status VARCHAR(32) NOT NULL,
  supporting_evidence_refs LONGTEXT,
  contradicting_evidence_refs LONGTEXT,
  missing_evidence LONGTEXT,
  rejected_reason TEXT,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  INDEX idx_hypotheses_task_id (task_id),
  INDEX idx_hypotheses_hypothesis_type (hypothesis_type),
  INDEX idx_hypotheses_status (status)
);

CREATE TABLE IF NOT EXISTS remediation_executions (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id BIGINT UNSIGNED NOT NULL,
  action_id VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL,
  risk_level VARCHAR(32),
  command_preview TEXT,
  dry_run_output LONGTEXT,
  approval_by VARCHAR(128),
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  INDEX idx_remediation_executions_task_id (task_id),
  INDEX idx_remediation_executions_action_id (action_id),
  INDEX idx_remediation_executions_status (status),
  INDEX idx_remediation_executions_risk_level (risk_level)
);

ALTER TABLE diagnosis_reports
  ADD COLUMN agent_execution_summary LONGTEXT NULL,
  ADD COLUMN agent_report_snapshot LONGTEXT NULL;
