# KubeSage RCA 评估框架 & Agent Trace 可视化

**创建日期**: 2026-05-31
**状态**: 待实现
**关联 Spec**: [LLM 防幻觉双重门禁](./2026-05-31-llm-hallucination-guard-design.md)

## 1. 问题定义

防幻觉改造解决了"LLM 不乱说"，但两个关键能力缺失：

1. **无法量化效果** — 没有评估框架证明改造后幻觉率真正下降
2. **无法可视化 Agent 思维链** — 前端看不到 Plan→Execute→Hypothesize→Reflect 的完整决策过程，demo 缺乏说服力

## 2. 子系统一：RCA 评估框架 (`cmd/rca-eval/`)

### 核心目标

面试时能说：**"在 N 个标注 case 上，改造前幻觉率 X%，改造后降至 Y%，准确率从 A% 提升到 B%。"**

### 2.1 Case 格式

每个评估 case 包含输入 + 期望输出：

```yaml
# eval/cases/oomkilled_memory_limit.yaml
id: "case-001"
name: "OOMKilled - 内存限制过低"
description: "Deployment 设置 memory limit=128Mi，实际负载使用 ~150Mi"
namespace: "default"
pod_name: "memory-hog-7d4f8b9c-x2k9"
expected:
  fault_type: "OOMKilled"
  root_cause: "容器内存限制(128Mi)低于实际使用量"
  key_evidences:
    - "last_state.terminated.reason == OOMKilled"
    - "exit_code == 137"
    - "memory_usage > memory_limit"
  should_not_contain:  # 防幻觉检查点
    - "内存泄漏"        # 本次是 limit 过低，不是泄漏
    - "连接池"          # 不相关
  acceptable_remediations:
    - "调整 memory limit"
    - "增加 memory request"
  risk_level: "medium"
  confidence_min: 0.7
```

### 2.2 评估指标

```go
type EvalMetrics struct {
    // 准确性指标
    RCAExactMatch       float64  // 根因完全命中 golden answer 的比例
    RCASemanticMatch    float64  // 语义匹配（宽松标准）
    FaultTypeAccuracy   float64  // 故障类型分类正确率

    // 幻觉指标（需要 LLM 增强开启）
    HallucinationRate      float64  // LLM 输出中含有证据外声明的比例
    EvidenceGroundingRate  float64  // LLM claim 可追溯到证据的平均比例
    AgreementWithRule      float64  // LLM 与规则引擎结论一致的比例

    // 安全性指标
    ActionSafetyRate    float64  // suggested_actions 中无危险命令的比例
    UnsafeActionCount   int

    // 置信度校准
    ConfidenceCalibration float64  // 预测置信度与实际准确率的相关性
    OverconfidentCount    int      // 高置信度但错误的 case 数

    // 综合
    CompositeScore      float64  // 加权综合分
}
```

### 2.3 评估 Runner

**关键设计决策**：评估不依赖真实 K8s 集群。采用 **Snapshot Replay** 模式 —— pre-collect 真实的 `DiagnosticContext` JSON 快照作为 fixture，eval runner 直接将 fixture 喂入 `DiagnosisEngine` 和 LLM 增强管线。这样可以在任何环境（CI、本地）重复运行，结果可复现。

```
cmd/rca-eval/
├── main.go              # CLI 入口：rca-eval run --suite=oomkilled --llm=on|off
├── loader.go            # 加载 YAML case 定义 + DiagnosticContext JSON fixture
├── runner.go            # 对每个 case：Deserialize fixture → 注水到引擎 → 收集 Report → 对比 golden
├── scorer.go            # 逐 case 打分，输出 EvalMetrics
├── reporter.go          # 输出 JSON/Markdown 对比报告
├── compare.go           # 对比两次 eval 结果：rca-eval compare before.json after.json
├── fixture.go           # DiagnosticContext + Report 序列化/反序列化工具
└── gen_fixture.go       # 从真实 K8s 生成 fixture 的工具命令：rca-eval gen-fixture

eval/
├── fixtures/
│   ├── oomkilled_memory_limit.json    # Diagnose() 前的完整 DiagnosticContext dump
│   ├── crashloop_config_error.json
│   └── ...
├── cases/
│   └── *.yaml                         # case 定义 + golden answer
└── results/
    └── baseline.json, before_guard.json, after_guard.json
```

**fixture 生成**（一次性操作，需要真实 K8s 集群或 demo Pod）：
```bash
# 部署 demo Pod，触发故障，生成 fixture
kubectl apply -f demo/oom.yaml
rca-eval gen-fixture --namespace=default --pod=memory-hog-xxx --output=eval/fixtures/oomkilled_memory_limit.json
```

**使用方式**：
```bash
# 关闭 LLM 增强，测纯规则引擎基线
rca-eval run --suite=oomkilled --llm=off --output=results/baseline.json

# 开启 LLM 增强（防幻觉关闭），测改造前
rca-eval run --suite=oomkilled --llm=on --grounding=off --output=results/before_guard.json

# 开启 LLM 增强 + 防幻觉，测改造后
rca-eval run --suite=oomkilled --llm=on --grounding=on --output=results/after_guard.json

# 对比报告
rca-eval compare results/before_guard.json results/after_guard.json --output=demo_comparison.md
```

### 2.4 初始 Case 集（至少 15 个）

| 故障类型 | Case 数量 | 覆盖场景 |
|----------|-----------|----------|
| OOMKilled | 4 | 内存限制过低、真实内存泄漏、Node 内存压力、误报（实际是 probe 超时） |
| CrashLoopBackOff | 4 | 配置错误(ConfigMap缺失)、启动命令错误、依赖服务不可达、Liveness探针失败 |
| Pod Pending | 3 | 资源不足无法调度、PVC 未绑定、NodeSelector 不匹配 |
| ImagePullBackOff | 2 | 镜像不存在、镜像仓库认证失败 |
| Probe Failed | 2 | Readiness 探针端口错误、探针超时 |

### 2.5 输出报告格式

对比报告示例（面试核心物料）：

```markdown
# KubeSage RCA 评估对比

| 指标 | 规则引擎基线 | LLM增强(无guard) | LLM增强(有guard) |
|------|-------------|-----------------|-----------------|
| RCA 准确率 | 82% | 76% ↓ | **91%** ↑ |
| 幻觉率 | 0% (纯规则) | 35% | **6%** |
| 证据锚定率 | N/A | 62% | **94%** |
| 危险建议率 | 0% | 8% | **0%** |
| 过度自信率 | 5% | 28% | **3%** |
| 综合分 | 85 | 63 | **93** |
```

## 3. 子系统二：Agent Trace 可视化

### 核心目标

面试 demo 时打开一个诊断任务，展示 **Agent 完整思维链**：Plan → 并行 Execute → Hypothesize → Reflect → Adjust → 最终结论。

### 3.1 数据层变更

**Agent Runtime 增加 Span Event 记录** (`internal/agent/runtime.go`)：

```go
// 在 Run() 的每个阶段注入 OpenTelemetry span event
func (r *Runtime) Run(ctx context.Context, opts RuntimeOptions) (*RunResult, error) {
    ctx, span := tracer.Start(ctx, "agent.run",
        trace.WithAttributes(
            attribute.Int("task.id", int(opts.TaskID)),
            attribute.String("agent.goal", opts.Goal.Summary()),
        ),
    )
    defer span.End()

    // Phase 1: Plan
    span.AddEvent("agent.plan", trace.WithAttributes(
        attribute.Int("plan.steps", len(plan.Steps)),
        attribute.String("plan.summary", plan.Summary),
    ))

    // Phase 2: Execute (per step)
    for _, step := range plan.Steps {
        stepSpan := tracer.Start(ctx, "agent.execute_step", ...)
        // ...
        stepSpan.AddEvent("agent.tool_result", trace.WithAttributes(
            attribute.String("tool.name", step.ToolName),
            attribute.Bool("tool.success", result.Success),
            attribute.String("tool.observation", truncate(result.Observation, 200)),
        ))
        stepSpan.End()
    }

    // Phase 3: Hypothesize
    span.AddEvent("agent.hypothesize", trace.WithAttributes(
        attribute.String("top_hypothesis", topHypothesis.Type),
        attribute.Float64("top_confidence", topHypothesis.Confidence),
    ))

    // Phase 4: Reflect
    span.AddEvent("agent.reflect", trace.WithAttributes(
        attribute.Bool("should_continue", result.ShouldContinue),
        attribute.String("reason", result.Reason),
    ))
}
```

### 3.2 持久化增强

`agent_steps` 表新增列：
```sql
ALTER TABLE agent_steps ADD COLUMN observation_summary VARCHAR(500);
ALTER TABLE agent_steps ADD COLUMN parallel_group INT DEFAULT 0;
ALTER TABLE agent_steps ADD COLUMN tool_latency_ms INT;
```

`agent.Runtime` 在每步完成后通过 `StepRecorder` 写入：
- `observation_summary` — 工具返回的截断摘要
- `parallel_group` — 并行执行组编号（同一批并行步骤共享相同 group ID）
- `tool_latency_ms` — 单步工具调用耗时

### 3.3 前端展示

`TaskDetail > Agent Timeline` 标签页改造为心智图风格的决策可视化：

```
┌─────────────────────────────────────────────────────────────┐
│  Agent 决策时间线                          总耗时: 6.3s     │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  📋 PHASE 1: PLAN                          耗时 0.8s       │
│  ├─ 规则诊断计划                                               │
│  │  ├─ 🔧 k8s.get_pod           [critical]      ✅ 0.3s  │
│  │  ├─ 🔧 k8s.get_events        [parallel]  ┐  ✅ 0.4s  │
│  │  └─ 🔧 k8s.get_previous_logs [parallel]  ┘  ✅ 0.6s  │
│  │                                                          │
│  📊 PHASE 2: HYPOTHESIZE                    耗时 0.2s       │
│  ├─ 🎯 OOMKilled (confidence: 0.87) ← top                   │
│  ├─ ⚪ 内存泄漏 (confidence: 0.45)                            │
│  └─ ⚪ Node压力 (confidence: 0.12)                            │
│  │                                                          │
│  🤔 PHASE 3: REFLECT                       耗时 0.5s       │
│  ├─ LLM: "证据充分，确认 OOMKilled"                             │
│  └─ 决策: should_continue=false                              │
│  │                                                          │
│  ✅ 结论: 容器内存限制过低 → OOMKilled                          │
│  │  置信度: 89%  风险: medium                                 │
│  │                                                          │
│  幻觉检测: ✅ 通过  (risk=0.12, grounding=94%)                │
└─────────────────────────────────────────────────────────────┘
```

### 3.4 API 增强

`GET /api/v1/tasks/:id` 的响应中 `report.agent_timeline` 字段扩展，包含 `parallel_group` 和 `observation_summary`。

## 4. 实现优先级

| 优先级 | 子系统 | 理由 |
|--------|--------|------|
| P0 | 防幻觉双重门禁 | 核心技术难题，先落地 |
| P0 | 评估框架 | 没有评估就没有故事闭环 |
| P1 | Agent Trace 可视化 | 前端展示，demo 加分项 |

## 5. 实现文件清单

### 评估框架（全新增）

| 文件 | 内容 |
|------|------|
| `cmd/rca-eval/main.go` | CLI 入口 |
| `cmd/rca-eval/loader.go` | Case YAML 加载 |
| `cmd/rca-eval/runner.go` | 评估 runner |
| `cmd/rca-eval/scorer.go` | 逐 case 打分 |
| `cmd/rca-eval/reporter.go` | JSON/Markdown 报告生成 |
| `cmd/rca-eval/compare.go` | 前后对比 |
| `eval/cases/*.yaml` | 15+ 评估 case |
| `eval/golden/*.json` | 标准答案 |

### Trace 可视化

| 文件 | 变更类型 | 内容 |
|------|----------|------|
| `internal/agent/runtime.go` | 修改 | 注入 OTEL span event |
| `internal/agent/recorder.go` | 修改 | observation_summary 写入 |
| `migrations/000004_agent_trace.up.sql` | 新增 | agent_steps 加列 |
| `web/src/pages/TaskDetail/tabs/AgentTimeline.tsx` | 修改 | 心智图风格可视化 |
| `web/src/api/types.ts` | 修改 | AgentTimeline 类型扩展 |
