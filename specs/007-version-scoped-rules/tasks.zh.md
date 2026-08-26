# 任务拆解：版本区间限定的知识包规则

> **特性 ID**：`007-version-scoped-rules` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有当该测试通过时才标记为 `done`。
此处提到的每一个测试都必须真实存在：`sddctl verify` 会检查这一点。

## 阶段 A —— 字段及其校验

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T601 | 在 Signal、LogPattern、FailureMode、Playbook、Step、Inspect 上新增 `VersionRange` | FR-001 | `TestEveryRuleKindAcceptsAVersionRange` | — | done |
| T602 | 区间的区间形式，以及偏向"判为重叠"的重叠检测 | FR-004 | `TestRangeOverlapDetection` | T601 | done |
| T603 | 仅当每次声明都带区间且互不重叠时才接受变体 | FR-003、FR-004 | `TestVariantsWithDisjointRangesAreAccepted`、`TestOverlappingVariantsAreRejected` | T602 | done |
| T604 | 错误码 `MAS-5016`…`MAS-5019`，双语，并重新生成文档 | CON-004 | `mas errcodes` 输出为最新 | T603 | done |
| **G-A** | **闸门 A** | | 带限定的包能加载；有歧义的包被拒 | | done |

## 阶段 B —— 解析

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T610 | `Pack.Resolve`：浅拷贝，丢弃超出区间的规则 | FR-002 | `TestOutOfRangeRulesAreDropped` | G-A | done |
| T611 | 未做限定的包解析后等于自身 | NFR-004 | `TestUnscopedPackResolvesToItself` | T610 | done |
| T612 | 按版本选择变体 | FR-005 | `TestVariantMatchingTheVersionIsChosen` | T610 | done |
| T613 | 版本未知时丢弃变体，并给出带处置建议的缺口 | FR-006 | `TestUnknownVersionDropsVariantsWithAGap` | T612 | done |
| T614 | 步骤随其依赖的 signal 与故障模式一并丢弃 | FR-007 | `TestStepsFollowTheRulesTheyDependOn` | T610 | done |
| T615 | 步骤随其读取的槽位一并丢弃，使用引擎自己的标识符扫描器 | FR-008 | `TestStepsFollowTheSlotsTheyRead` | T614 | done |
| T616 | 已无存活结论的 playbook 被丢弃 | FR-009 | `TestEmptyPlaybooksAreDropped` | T615 | done |
| T617 | 每一次丢弃都是带错误码、细节与影响的缺口 | FR-010、CON-002 | `TestSkippedRulesAreRecordedAsGaps` | T616 | done |
| T618 | 解析绝不放宽，覆盖一张版本表 | FR-011、CON-001 | `TestResolutionNeverWidens` | T616 | done |
| **G-B** | **闸门 B** | | `go test ./internal/knowledge/...` | | done |

## 阶段 C —— 运行

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T620 | service 在准入阶段解析一次，并带上缺口 | FR-012、NFR-001 | `TestDiagnosisUsesTheResolvedPack` | G-B | done |
| T621 | Kafka 的 ZooKeeper 与 KRaft 边界，只依据有据可查的事实 | FR-014 | `TestKafkaPackScopesZooKeeperRules` | T620 | done |
| T622 | 在 Kafka 4.0 边界两侧各一个语料库 case | FR-014、NFR-004 | `TestShippedCorpusPasses` | T621 | done |
| **G-C** | **闸门 C** | | `make eval` 无回归；语料库行为不变 | | done |

## 阶段 D —— 界面与文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T630 | `mas packs --show --version` 预览解析结果 | FR-013 | `TestPacksCommandShowsVersionScoping` | G-C | done |
| T631 | 关于区间与变体的双语知识包编写指引 | NFR-003、NFR-002 | `sddctl verify` 对等检查；`go.mod` 未变 | T630 | done |
| **G-D** | **闸门 D —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T601–T604 | `go test ./internal/knowledge/...` |
| G-B | T610–T618 | `go test ./internal/knowledge/...` |
| G-C | T620–T622 | `make eval` |
| G-D | T630–T631 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版任务拆解 | 代码、知识包、文档 |
