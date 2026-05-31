ALTER TABLE agent_steps
  DROP INDEX idx_agent_steps_parallel_group,
  DROP COLUMN tool_latency_ms,
  DROP COLUMN parallel_group,
  DROP COLUMN observation_summary;
