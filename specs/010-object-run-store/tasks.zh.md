# 任务拆解：基于对象存储的共享持久化运行存储

> **特性 ID**：`010-object-run-store` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有当该测试通过时才标记为 `done`。
此处提到的每一个测试都必须真实存在：`sddctl verify` 会检查这一点。

## 阶段 A —— 签名

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T901 | 依据规范手写 SigV4，仅用标准库 | FR-002、NFR-001 | `TestSigV4MatchesPublishedVectors` | — | done |
| T902 | 错误码 `MAS-6010`…`MAS-6014`，双语，并重新生成文档 | CON-004 | `mas errcodes` 输出为最新 | T901 | done |
| **G-A** | **闸门 A** | | 公开测试向量全部通过 | | done |

## 阶段 B —— 客户端

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T910 | 基于 `net/http` 的 PUT、GET 与 LIST，对着会校验签名的桩服务端 | FR-001、NFR-004 | `TestS3StoreSatisfiesTheContract` | G-A | done |
| T911 | path-style 与 virtual-host 两种寻址方式 | FR-010 | `TestBothAddressingStylesAreSupported` | T910 | done |
| T912 | 非 2xx 变成携带 S3 错误码的带码错误 | FR-007、CON-002 | `TestStorageFailuresAreCoded` | T910 | done |
| **G-B** | **闸门 B** | | `go test ./internal/store/...` | | done |

## 阶段 C —— 存储本体

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T920 | 对象布局；Create、Append、Finish、Fail | FR-001、FR-003、CON-001 | `TestStepsAreWrittenAsImmutableObjects` | G-B | done |
| T921 | Get，含被中断运行的重建 | FR-004、NFR-003 | `TestInterruptedRunIsReconstructed` | T920 | done |
| T922 | List：最新优先、有上限，且不读取任何不会被返回的记录 | FR-005 | `TestListIsNewestFirstAndBounded` | T920 | done |
| T923 | 完整性摘要能原样往返 | FR-006 | `TestDigestSurvivesTheRoundTrip` | T920 | done |
| **G-C** | **闸门 C** | | `go test ./internal/store/...` | | done |

## 阶段 D —— 配置、服务与文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T930 | `StoreConfig.S3`，加载时校验；凭据为密钥 | FR-012、FR-009、CON-003 | `TestObjectStoreConfigIsValidatedAtLoad`、`TestObjectStoreCredentialsAreNeverEchoed` | G-C | done |
| T931 | `store.Open` 新增 `s3`；其他存储丝毫未动 | FR-013 | `TestExistingStoresAreUnchanged` | T930 | done |
| T932 | 存储失败时运行仍然完成，报告完好 | FR-008 | `TestRunSurvivesAStoreFailure` | T931 | done |
| T933 | `mas doctor` 探测该桶 | FR-011 | `TestDoctorProbesTheObjectStore` | T931 | done |
| T934 | 双语文档：配置参考、用户手册、部署示例 | NFR-002 | `sddctl verify` 对等检查 | T933 | done |
| **G-D** | **闸门 D —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T901–T902 | `go test ./internal/store/...` |
| G-B | T910–T912 | `go test ./internal/store/...` |
| G-C | T920–T923 | `go test ./internal/store/...` |
| G-D | T930–T934 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版任务拆解 | 代码、配置、文档 |
