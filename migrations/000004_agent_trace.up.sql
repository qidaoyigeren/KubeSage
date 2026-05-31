ALTER TABLE agent_steps
  ADD COLUMN observation_summary TEXT NULL,
  ADD COLUMN parallel_group VARCHAR(128) NULL,
  ADD COLUMN tool_latency_ms BIGINT NULL,
  ADD INDEX idx_agent_steps_parallel_group (parallel_group);
