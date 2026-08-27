# 详细设计（LLD）：基于对象存储的共享持久化运行存储

> **特性 ID**：`010-object-run-store` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

```
internal/store/
  sigv4.go     新增：AWS Signature Version 4，依据规范实现
  s3.go        新增：最小 S3 客户端 —— PUT、GET、LIST
  object.go    新增：构建在该客户端之上的 RunStore
  fs.go        Open 新增 "s3" 分支；其余不变
internal/config/
  config.go    + StoreConfig.S3
  validate.go  + 加载时校验
internal/service/
  doctor.go    + 对象存储探测
pkg/errs/
  registry.go  MAS-6010…MAS-6014
```

## 2. 配置

```yaml
store:
  type: s3
  s3:
    endpoint: https://s3.eu-west-1.amazonaws.com   # 或 http://minio:9000
    region: eu-west-1
    bucket: mas-runs
    prefix: prod                                    # 可选
    access_key_id: "${env:MAS_S3_KEY_ID}"
    secret_access_key: "${env:MAS_S3_SECRET}"
    path_style: true                                # MinIO 与多数自建部署
    timeout: 30s
```

```go
type S3Config struct {
    Endpoint        string   `yaml:"endpoint"`
    Region          string   `yaml:"region"`
    Bucket          string   `yaml:"bucket"`
    Prefix          string   `yaml:"prefix"`
    AccessKeyID     Secret   `yaml:"access_key_id"`
    SecretAccessKey Secret   `yaml:"secret_access_key"`
    PathStyle       bool     `yaml:"path_style"`
    Timeout         Duration `yaml:"timeout"`
}
```

两个凭据都是 `Secret`，因此它们在日志、`mas config` 与 JSON 中都无法被打印，
并且支持 `${env:}` 与 `${file:}` 引用（FR-009）。

加载时的校验（FR-012），以 `MAS-6010` 报告：
`endpoint` 必须能解析为绝对 URL；`region` 与 `bucket` 必须设置；
两个凭据必须同时设置或同时留空 ——
只配了一半，意味着运维人员以为自己配好了访问权限，而其实没有。

## 3. SigV4

```go
// Sign 为请求补上所需的 Authorization 与 x-amz-* 头。
func Sign(req *http.Request, payloadSHA256, accessKeyID, secret, region, service string, now time.Time) error
```

严格按规范的四步走：规范化请求、待签字符串、签名密钥、签名。
全部内容只用到 `crypto/sha256`、`crypto/hmac` 与 `encoding/hex`。

值得点名的几处，因为各实现之间的差异恰恰出在这里：

- URI 路径**按段**转义，且 `/` 不被转义；
- 查询参数先按名排序、同名内再按值排序，两者都按 RFC 3986 规则转义 ——
  `+` 是 `%2B`，空格是 `%20`；
- 头名小写，头值去首尾空白并把内部连续空格折叠为一个，签名头列表要排序；
- payload 哈希是请求体的十六进制 SHA-256，同时也作为 `x-amz-content-sha256` 发送。

`TestSigV4MatchesPublishedVectors` 跑的就是规范自带的测试套件用例：
它们的存在，正是为了抓住上面这四条 ——
这也是为什么这个测试比实现本身更有价值。

## 4. 客户端

是三个动词而不是四个：初稿里有一个"存在性检查"，而 `Get` 本身就是一个，
于是它被删掉了，而不是留在那里不被使用。

是三个动词而不是四个：初稿里有一个“存在性检查”，而 `Get` 本身就是一个，
于是它被删掉了，而不是留在那里不被使用。

```go
type s3Client struct {
    cfg  config.S3Config
    http *http.Client
}

func (c *s3Client) put(ctx context.Context, key string, body []byte) error
func (c *s3Client) get(ctx context.Context, key string) ([]byte, error)
func (c *s3Client) list(ctx context.Context, prefix, delimiter, after string, max int) (listResult, error)
```

`list` 用 `encoding/xml` 解析 ListObjectsV2 的响应 —— 四个字段，
外加让"带分隔符的列举"表现得像目录的 `CommonPrefixes`。

寻址方式（FR-010）：path-style 把桶放在路径里
（`http://minio:9000/mas-runs/key`），virtual-host 把桶放在主机名里
（`https://mas-runs.s3.region.amazonaws.com/key`）。
path-style 是自建部署的默认，也是大多数人实际在跑的方式。

非 2xx 响应会变成 `MAS-6011`，携带状态码与响应体中的 S3 错误码 ——
因为 "AccessDenied" 与 "NoSuchBucket" 对应的处置完全不同，而两者都在响应里。

## 5. 对象布局

```
<prefix>/runs/<runID>/record.json
<prefix>/runs/<runID>/steps/0001.json
```

步骤键左侧补零到四位，因此字典序就是步骤序。
超过 9999 个步骤的运行不是本项目能产生的 —— 步数预算比它小三个数量级 ——
但写入方会返回 `MAS-6014` 而不是回绕，
因为让一份审计轨迹被静默重排，比直接拒绝更糟。

| 操作 | 它做什么 |
|---|---|
| `Create` | PUT `record.json`，状态为 `running` |
| `Append` | PUT `steps/NNNN.json`，完全不碰 `record.json` |
| `Finish` | PUT `record.json`，带报告、用量、状态与摘要 |
| `Fail` | PUT `record.json`，状态为 `failed` 并带错误码 |
| `Get` | GET `record.json`；若状态为 `running`，则 LIST 步骤并合并 |
| `List` | 以 `delimiter=/` LIST `runs/`，最新优先，然后逐个 GET 记录 |

`Append` 在内存中维护每次运行的计数器。
第二个进程向同一次运行追加会发生冲突 —— 而这不可能发生，
因为一次运行属于执行它的那个进程；
副本之间共享的是已完成的记录，不是进行中的运行（HLD §5）。

## 6. Get，以及被中断的运行

一次已完成的运行只需一次 GET，这就是 NFR-003。

当 `record.json` 显示 `running` 时，写下它的那个进程要么已经消失、要么仍在跑，
而步骤才是事实。它们会被列举并合并，且返回的记录**保持 `status: running`**：
它是被记录下来的样子，而不是"这次运行完成了"的主张。
`TestInterruptedRunIsReconstructed` 会同时断言合并与状态 ——
因为把一次重建当作已完成的运行呈现，就是这套装置在替它编一个结局（RSK-3）。

## 7. List

运行 id 形如 `run-<时间戳>-<随机>`，因此字典序倒排就是最新优先 ——
无需维护索引，也就没有索引会与事实脱节（D-6）。

列举会一直翻页，直到拿到 `limit` 个运行前缀为止，然后才去 GET 它们的记录。
一条不会被返回的记录，永远不会被读取（RSK-4）。

## 8. 失败

每一个非 2xx、每一次传输错误，都会变成一个带错误码的错误，
并连同运行 id 记入日志。service 已经把"分析完成之后的存储错误"视为非致命：
报告照常返回，失败情况记录在旁边 ——
因为"没能把答案归档就把答案一起丢掉"，在故障处置中是错误的交易（FR-008）。

## 9. 错误码

| 错误码 | 含义 |
|---|---|
| `MAS-6010` | 对象存储配置非法 |
| `MAS-6011` | 对象存储返回了错误 |
| `MAS-6012` | 对象存储不可达 |
| `MAS-6013` | 存储中的某条记录格式错误 |
| `MAS-6014` | 某次运行的步骤数超出了键布局可排序的范围 |

## 10. 测试

| 测试 | 它钉住了什么 |
|---|---|
| `TestSigV4MatchesPublishedVectors` | FR-002、CON-005 |
| `TestS3StoreSatisfiesTheContract` | FR-001 |
| `TestStepsAreWrittenAsImmutableObjects` | FR-003、CON-001 |
| `TestInterruptedRunIsReconstructed` | FR-004、RSK-3 |
| `TestListIsNewestFirstAndBounded` | FR-005、RSK-4 |
| `TestDigestSurvivesTheRoundTrip` | FR-006 |
| `TestStorageFailuresAreCoded` | FR-007、CON-002 |
| `TestRunSurvivesAStoreFailure` | FR-008 |
| `TestObjectStoreCredentialsAreNeverEchoed` | FR-009、CON-003 |
| `TestBothAddressingStylesAreSupported` | FR-010 |
| `TestDoctorProbesTheObjectStore` | FR-011 |
| `TestObjectStoreConfigIsValidatedAtLoad` | FR-012 |
| `TestExistingStoresAreUnchanged` | FR-013 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版详细设计 | tasks、代码 |
