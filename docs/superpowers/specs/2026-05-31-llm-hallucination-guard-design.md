# KubeSage LLM 输出防幻觉双重门禁系统

**创建日期**: 2026-05-31
**状态**: 待实现
**关联**: `internal/llm/prompt.go`, `internal/llm/client.go`, `internal/llm/types.go`

## 1. 问题定义

### 当前问题

LLM（DeepSeek / OpenAI-compatible）在增强诊断报告时会出现"加戏"行为：

1. **虚构因果关系** — 规则引擎只报告"内存不足"，LLM 编造"可能是 Redis 连接池泄漏导致的内存碎片化"
2. **证据权重失真** — LLM 将低权重证据提升为核心原因
3. **建议越界** — LLM 添加与诊断无关的操作建议

### 根因

当前 LLM 输出只有**结构性 sanitization**（JSON 格式校验、置信度 clamp、危险命令正则过滤），**没有事实性验证**。System prompt 建议"以规则结果为准"不构成约束力。

### 影响范围

- `internal/llm/prompt.go` — `BuildPrompt()` 和 `systemPrompt()`
- `internal/llm/client.go` — `GenerateDiagnosisSummary()` 的响应处理
- `internal/llm/types.go` — `EnhancedSummary` 结构体
- `internal/service/diagnosis_service.go` — `enhanceReportWithLLM()` 的调用和降级逻辑

## 2. 设计方案

### 核心思路

**Layer 1 — 结构化输出锚定**：改变 LLM 职责，从"自由总结"变为"填模板"。每个声明必须显式引用可溯源的证据 ID。

**Layer 2 — 证据锚定验证管线**：LLM 输出经三道程序化校验后才准入最终报告。

### 架构图

```
                    ┌──────────────────┐
                    │  Rule Engine     │
                    │  report.Evidences│
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │  Layer 1: Build  │
                    │  Structured      │
                    │  Prompt with     │
                    │  Evidence IDs    │
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │  LLM Calling     │
                    │  (OpenAI-        │
                    │   Compatible)    │
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │  Layer 2:        │
                    │  Validation      │
                    │  Pipeline        │
                    │                  │
                    │  ① Reference     │
                    │     Resolution   │──fail──► 剔除 claim
                    │                  │
                    │  ② Semantic      │──fail──► 标记 [unverified]
                    │     Grounding    │
                    │                  │
                    │  ③ Agreement     │──disagree► review queue
                    │     Gate         │
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │  hallucination   │
                    │  _risk > 0.4 ?   │
                    │                  │
                    │  YES → 降级为    │
                    │     纯规则报告   │
                    │  NO → 采信LLM    │
                    │     增强报告     │
                    └──────────────────┘
```

## 3. 数据结构设计

### 新增类型 (internal/llm/types.go)

```go
// GroundedSummary 替代现有的 EnhancedSummary
type GroundedSummary struct {
    // Layer 1: 结构化根因确认（LLM不能修改rule_diagnosis）
    RootCauseConfirmation RootCauseConfirmation `json:"root_cause_confirmation"`

    // Layer 1: 证据链（每个声明必须引用证据ID）
    EvidenceChain []GroundedClaim `json:"evidence_chain"`

    // Layer 1: LLM 的额外观察（可选，会自动标记unverified）
    AdditionalObservations []string `json:"additional_observations,omitempty"`

    // Layer 1: 建议动作
    SuggestedActions []string `json:"suggested_actions"`
}

type RootCauseConfirmation struct {
    RuleDiagnosis  string `json:"rule_diagnosis"`  // 代码注入，LLM只读
    AgreementLevel string `json:"agreement_level"` // full_agree | partial_agree | disagree
    Explanation    string `json:"explanation"`
}

type GroundedClaim struct {
    Claim              string   `json:"claim"`
    EvidenceRefs       []string `json:"evidence_refs"` // 必须≥1个
    ConfidenceIncrement float64 `json:"confidence_increment"` // 0.0-1.0
}

// ValidationResult 管线校验结果
type ValidationResult struct {
    Passed               bool              `json:"passed"`
    HallucinationRisk    float64           `json:"hallucination_risk"`
    GroundingScore       float64           `json:"grounding_score"`       // 已验证claim / 总claim
    AgreementScore       float64           `json:"agreement_score"`       // 与规则结论的一致性
    UnresolvedRefs       []string          `json:"unresolved_refs"`       // 不存在的证据ID
    UngroundedClaims     []UngroundedClaim `json:"ungrounded_claims"`     // 语义锚定失败的claim
    AgreementIssue       string            `json:"agreement_issue"`       // agree_gate 问题描述
    FallbackReason       string            `json:"fallback_reason"`       // 降级原因
}

type UngroundedClaim struct {
    Claim        string  `json:"claim"`
    Similarity   float64 `json:"similarity"`   // 最高的语义相似度
    EvidenceRefs []string `json:"evidence_refs"`
}
```

### Prompt 模板变更 (internal/llm/prompt.go)

新增 `BuildGroundedPrompt()`，在 `promptPayload` 中注入：

```go
type promptEvidence struct {
    // 新增字段
    EvidenceID string `json:"evidence_id"` // 如 "ev-0", "ev-1"，用于 LLM 引用
    // ... 原有字段
}
```

System prompt 核心约束：

```
"You are KubeSage's evidence-grounded RCA report writer."
"1. root_cause_confirmation.rule_diagnosis is injected by the system. DO NOT modify it."
"2. agreement_level MUST be one of: full_agree, partial_agree, disagree."
"3. For every claim in evidence_chain, you MUST reference >=1 evidence_id from the evidence list."
"4. Do NOT invent facts not present in the referenced evidence."
"5. If you cannot ground a claim in existing evidence, place it in additional_observations instead."
```

## 4. 管线校验逻辑

### Check ①: Reference Resolution

```go
func (v *EvidenceValidator) validateReferences(summary *GroundedSummary, evidenceIDs map[string]bool) []string
```

- 遍历所有 `GroundedClaim.EvidenceRefs`
- 检查每个 ref 是否存在于注入的证据 ID 集合中
- 不存在的 ref → 记录到 `ValidationResult.UnresolvedRefs`
- 该 claim 直接剔除（不贡献置信度）

### Check ②: Semantic Grounding

```go
func (v *EvidenceValidator) checkGrounding(claim GroundedClaim, evidences map[string]string) (float64, bool)
```

- 对每个 claim，拼接其引用的证据文本
- 计算 claim 文本与证据文本的语义重叠度
- **两级策略**：
  - **快速路径**：关键词重叠度（Jaccard similarity），适合所有场景
  - **精确路径**：若配置了 embedding API，使用余弦相似度
- 阈值: `grounding_threshold = 0.3`（可配置）
- 低于阈值的 claim → 标记 `[unverified]`，移入 `UngroundedClaims`

### Check ③: Agreement Gate

```go
func (v *EvidenceValidator) checkAgreement(summary *GroundedSummary, ruleResult RuleBasedResult) (float64, string)
```

- 检查 `agreement_level`
  - `full_agree` → agreement_score = 1.0
  - `partial_agree` → agreement_score = 0.6
  - `disagree` → agreement_score = 0.0，记录 agreement_issue
- 额外检查：LLM 的 `root_cause_summary` 关键实体（故障类型、组件名）是否存在于规则结论中

### Hallucination Risk 计算

```go
grounding_score   = max(0, len(verified_claims)) / max(1, len(total_claims))
agreement_score   = // 来自 Check ③
hallucination_risk = 1.0 - (grounding_score × 0.6 + agreement_score × 0.4)
```

## 5. 降级策略矩阵

| 条件 | 行为 |
|------|------|
| LLM API 调用失败 | 回退为纯 `RuleBasedResult`，emit metric `llm_enhancement_failed` |
| `hallucination_risk > 0.4` | 丢弃 LLM 输出，使用 `RuleBasedResult`，写入 `audit_log`，emit metric `llm_hallucination_detected` |
| `hallucination_risk > 0.3 && <= 0.4` | 保留 LLM 输出但标记 `quality_warning`, 前端显示"LLM 增强部分可能不准确" |
| `agreement_level = disagree` | agreement_score=0，是否降级由联合公式决定：若 claims 全量锚定（grounding=1.0）则 risk=0.4 边界通过；若 grounding 也弱则自动跌入降级区间。这意味着"有充分证据的不同意见"可保留，"无证据的反对"被驳回 |
| `grounding_score < 0.3` | 直接触发降级（大部分声明无法锚定 = 严重幻觉） |
| `grounding_score >= 0.5 && hallucination_risk <= 0.2` | 全量采信，正常展示 |

以上阈值均可通过 `configs/config.yaml` 配置：

```yaml
llm:
  grounding:
    enabled: true
    semantic_threshold: 0.3      # Check ② 的阈值
    hallucination_risk_threshold: 0.4  # 触发降级的阈值
    quality_warning_threshold: 0.3    # 触发 quality_warning 的阈值
    grounded_claim_threshold: 0.3     # 单 claim grounding 阈值
```

## 6. 向后兼容

- 原 `EnhancedSummary` 保留，新增 `GroundedSummary`
- `GroundedSummary` 实现 `ToEnhancedSummary()` 方法，降级时转换
- `report.LLMEnhancedSummary` 字段不变（interface{}），新的 Grounded 类型可赋值
- 前端无需变更：`GroundedSummary.ToEnhancedSummary()` 提供相同 Schema 的 JSON
- 新 Prompt 通过 `config.LLMConfig.Grounding.Enabled` 开关控制，默认启用

## 7. 测试策略

### 单元测试 (grounding_test.go)

| 测试用例 | 输入 | 期望输出 |
|----------|------|----------|
| `TestValidateReferences_AllResolved` | 所有 ref 有效 | UnresolvedRefs=[] |
| `TestValidateReferences_PartialResolved` | 部分 ref 无效 | UnresolvedRefs 包含无效 ID，该 claim 被剔除 |
| `TestValidateReferences_NoEvidence` | 空 evidence 列表 | 全部 claim 被标记 ungrounded |
| `TestCheckGrounding_HighOverlap` | claim 与证据高度重叠 | Pass, similarity>0.5 |
| `TestCheckGrounding_HallucinatedClaim` | claim 与证据无交集 | Fail, 标记 unverified |
| `TestCheckAgreement_FullAgree` | agreement=full_agree | score=1.0 |
| `TestCheckAgreement_Disagree` | agreement=disagree | score=0.0, 记录 issue |
| `TestHallucinationRisk_High` | grounding=0.2, agreement=0 | risk>0.4, 触发降级 |
| `TestHallucinationRisk_Low` | grounding=1.0, agreement=1.0 | risk=0, 通过 |
| `TestFallbackPipeline` | LLM 返回空/无效 JSON | 降级到 RuleBasedResult |
| `TestEndToEnd_RealWorld` | 完整 OOMKilled 诊断→LLM 增强→校验 | 最终报告不包含编造内容 |

### 集成测试

- Mock LLM 返回已知 hallucination pattern，验证管线正确检测并降级
- Mock LLM 返回正确 grounded 输出，验证全量采信路径
- Mock LLM 返回 disagree + 全量锚定，验证 risk=0.4 边界通过（不触发 >0.4 丢弃）
- Mock LLM 返回 disagree + 低锚定，验证由公式触发降级

## 8. 可观测指标

新增 Prometheus 指标：

```
kubesage_llm_grounding_total{result="passed|degraded|rejected"}  # 管线结果计数
kubesage_llm_hallucination_risk{range="0-0.2|0.2-0.4|0.4-0.6|0.6+"}  # 幻觉风险分布
kubesage_llm_evidence_refs_unresolved  # 未解析的证据引用总数
kubesage_llm_claims_ungrounded  # 无法语义锚定的声明总数
kubesage_llm_fallback_total{reason="api_error|hallucination_risk|grounding_score"}  # 降级计数
```

## 9. 实现文件清单

| 文件 | 变更类型 | 内容 |
|------|----------|------|
| `internal/llm/types.go` | 修改 | 新增 GroundedSummary, GroundedClaim, RootCauseConfirmation, ValidationResult |
| `internal/llm/prompt.go` | 修改 | 新增 BuildGroundedPrompt(), 重写 systemPrompt() |
| `internal/llm/client.go` | 修改 | 新增 GenerateGroundedSummary(), 解析新的 JSON Schema |
| `internal/llm/grounding.go` | **新增** | EvidenceValidator + 三道校验逻辑 |
| `internal/llm/grounding_test.go` | **新增** | 完整单元测试 |
| `internal/config/config.go` | 修改 | 新增 LLMGroundingConfig 结构体 |
| `configs/config.yaml` | 修改 | 新增 llm.grounding 配置段 |
| `internal/service/diagnosis_service.go` | 修改 | `enhanceReportWithLLM()` 调用新的 grounded 路径 + 降级逻辑 |
| `deployments/helm/kubesage/values.yaml` | 修改 | 对应 Helm values |
