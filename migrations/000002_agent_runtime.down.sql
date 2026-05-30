ALTER TABLE diagnosis_reports
  DROP COLUMN agent_report_snapshot,
  DROP COLUMN agent_execution_summary;

DROP TABLE IF EXISTS remediation_executions;
DROP TABLE IF EXISTS hypotheses;
DROP TABLE IF EXISTS agent_steps;
