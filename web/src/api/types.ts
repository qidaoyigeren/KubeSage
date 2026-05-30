export type TaskStatus = 'pending' | 'running' | 'success' | 'failed';
export type Severity = 'info' | 'warning' | 'critical' | 'debug';
export type RiskLevel = 'low' | 'medium' | 'high' | 'forbidden';
export type HypothesisStatus = 'active' | 'rejected' | 'confirmed';
export type RemediationStatus =
  | 'proposed'
  | 'blocked'
  | 'dry_run_pending'
  | 'dry_run_success'
  | 'dry_run_failed'
  | 'pending_approval';
export type AgentStepStage =
  | 'plan'
  | 'tool_call'
  | 'observation'
  | 'reflection'
  | 'decision'
  | 'action'
  | 'verification';
export type AgentStepStatus = 'success' | 'failed' | 'skipped';

export interface Evidence {
  id: number;
  task_id: number;
  source_type: string;
  title: string;
  content: string;
  severity: Severity;
  raw_json: string;
  created_at: string;
}

export interface AgentStep {
  id: number;
  task_id: number;
  parent_step_id?: number | null;
  trace_id: string;
  step_index: number;
  stage: AgentStepStage;
  tool_name?: string;
  input_json?: string | null;
  output_json?: string | null;
  status: AgentStepStatus;
  duration_ms: number;
  reasoning_summary: string;
  created_at: string;
}

export interface Hypothesis {
  id: number;
  task_id: number;
  hypothesis_type: string;
  summary: string;
  confidence_score: number;
  status: HypothesisStatus;
  supporting_evidence_refs?: string[] | null;
  contradicting_evidence_refs?: string[] | null;
  missing_evidence?: string[] | null;
  rejected_reason?: string;
  updated_at: string;
  created_at: string;
}

export interface RemediationExecution {
  id: number;
  task_id: number;
  action_id: string;
  status: RemediationStatus;
  risk_level: RiskLevel;
  command_preview: string;
  dry_run_output: string;
  approval_by?: string;
  created_at: string;
  updated_at: string;
}

export interface RemediationAction {
  action_id: string;
  action_type: string;
  description: string;
  command_preview: string;
  risk_level: RiskLevel;
  need_human_confirm: boolean;
  executable: boolean;
}

export interface VerificationPlanItem {
  action_id: string;
  what_to_check: string;
  tool_to_use: string;
  success_condition: string;
  timeout_seconds: number;
}

export interface AgentReportSnapshot {
  rule_based_result?: string;
  agent_execution_summary?: string;
  hypotheses?: Hypothesis[];
  evidence_chain?: { ref: string; source_type: string; title: string; severity: string }[];
  runbook_guidance?: { title: string; content: string; score: number; recommended_tools?: string[]; stop_conditions?: string[] }[];
  llm_enhanced_summary?: string;
  remediation_actions?: RemediationAction[];
  remediation_executions?: RemediationExecution[];
  verification_plan?: VerificationPlanItem[];
  residual_risks?: string[];
  stop_reason?: string;
}

export interface DiagnosisReport {
  id: number;
  task_id: number;
  namespace: string;
  pod_name: string;
  fault_type: string;
  root_cause_summary: string;
  confidence_score: number;
  impact_analysis: string;
  suggested_actions: string;
  remediation_actions: RemediationAction[] | null;
  risk_level: RiskLevel;
  need_human_confirm: boolean;
  rule_based_result: string;
  llm_enhanced_summary: string;
  agent_execution_summary: string;
  agent_report_snapshot: AgentReportSnapshot | null;
  created_at: string;
  generated_at: string;
  evidences?: Evidence[];
  agent_timeline?: AgentStep[];
  hypotheses?: Hypothesis[];
  remediation_executions?: RemediationExecution[];
}

export interface DiagnosisTask {
  id: number;
  namespace: string;
  pod_name: string;
  status: TaskStatus;
  alert_name: string;
  alert_severity: string;
  fault_type: string;
  root_cause_summary: string;
  confidence_score: number;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
  evidences?: Evidence[];
  report?: DiagnosisReport;
}

export interface PaginatedResponse<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface DiagnoseRequest {
  namespace: string;
  pod_name: string;
  container_name?: string;
  expected_fault_type?: string;
  alert_name?: string;
  alert_severity?: string;
  include_logs?: boolean;
  include_events?: boolean;
  include_metrics?: boolean;
}

export interface DashboardBucket {
  name: string;
  count: number;
  rate?: number;
}

export interface DashboardSummary {
  total_tasks: number;
  running_tasks: number;
  success_tasks: number;
  failed_tasks: number;
  success_rate: number;
  average_duration_seconds: number;
  feedback_useful: number;
  feedback_not_useful: number;
  feedback_accuracy_rate: number;
  top_root_causes: DashboardBucket[] | null;
  analyzer_hit_rates: DashboardBucket[] | null;
  llm_calls: Record<string, number>;
  llm_total_tokens: number;
  llm_average_latency_ms: number;
  llm_estimated_cost: number;
}

export interface DashboardTrendPoint {
  date: string;
  fault_type: string;
  count: number;
}

export interface FeedbackRequest {
  rating: 'useful' | 'not_useful';
  corrected_root_cause?: string;
  helpful_evidence_refs?: string[];
  helpful_tool_names?: string[];
  helpful_hypothesis_types?: string[];
  comment?: string;
}
