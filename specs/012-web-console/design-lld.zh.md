# 详细设计（LLD）：只读 Web 控制台

> **特性 ID**：`012-web-console` · **版本**：1.0.1 · **状态**：已批准
> **双语对照**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

```
internal/httpapi/
  console.go          新增：资源白名单、处理器、CSP
  strings.go          新增：双语 UI 字符串表
  assets/
    index.html        新增：外壳 —— 无数据、无内联脚本与样式
    app.css           新增
    app.js            新增：整个客户端
  server.go           + 控制台路由、+ 索引上的 `language`，
                        Routes() 改为报告实际注册了什么
  auth.go             + anonymousRoutes 中的控制台路径
internal/config/
  config.go           + ServerConfig.UI
  validate.go         （无改动：一个布尔值没有非法取值）
internal/service/
  doctor.go           + 控制台检查项
pkg/errs/
  registry.go         + MAS-7016
```

## 2. 配置

```yaml
server:
  addr: ":8080"
  ui:
    enabled: true      # 默认值；设为 false 则完全不提供控制台
```

```go
// UIConfig 配置 Web 控制台。
type UIConfig struct {
    // Enabled 默认为 true，这正是它用 *bool 的原因：普通 bool 无法区分
    // "运维写了 false" 与 "运维什么也没写"，而在这里两者必须意味着相反的事。
    Enabled *bool `yaml:"enabled" json:"enabled,omitempty"`
}

func (u UIConfig) On() bool { return u.Enabled == nil || *u.Enabled }
```

指针是这份配置里唯一的微妙之处，而且是被逼出来的：一个默认为真的布尔值，从 YAML 读进 `bool`
时，"键不存在"和"显式写了 false"读起来完全一样。因此 `config.Default()` 不设置它 —— 缺席
**就是**默认 —— 而 `On()` 是唯一的读取点。

## 3. 资源与白名单

```go
//go:embed assets/index.html assets/app.css assets/app.js
var assetFS embed.FS

// consoleAssets 是白名单，而不是架在 assetFS 上的文件服务器：
// 默认拒绝意味着"往目录里加一个文件"不等于"把它发布出去"。
var consoleAssets = map[string]struct{ file, mime string }{
    "":            {"assets/index.html", "text/html; charset=utf-8"},
    "index.html":  {"assets/index.html", "text/html; charset=utf-8"},
    "app.css":     {"assets/app.css", "text/css; charset=utf-8"},
    "app.js":      {"assets/app.js", "text/javascript; charset=utf-8"},
}
```

`strings.json` 不在这张表里：它在每次请求时由 Go 生成，而不是内嵌文件，因此那张表与控制台
实际收到的内容不可能发生分歧。

`handleConsole` 取 `/ui/` 之后的路径去查表，其余一律返回 `MAS-7404`。不带斜杠的 `/ui`
重定向到 `/ui/`，以便相对资源引用能正确解析。

每个控制台响应都携带：

```
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self';
                         connect-src 'self'; img-src 'self' data:; base-uri 'none';
                         form-action 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cache-Control: no-store        （strings.json 上；资源为 max-age=0，版本号带在 URL 查询里）
```

`default-src 'none'` 意味着凡未列出者一律拒绝 —— 这与安全守卫、与路由表已经采取的默认拒绝
姿态是同一个。`connect-src 'self'` 把 `fetch` 限制在本源，因此即便真被找到某个注入点，凭据
也无法被发往别处。`frame-ancestors 'none'` 阻断点击劫持，而 `form-action 'none'` 则是诚实的
—— 控制台压根没有任何表单提交。

## 4. 匿名，说清楚

```go
var anonymousRoutes = map[string]bool{
    "/healthz": true,
    "/readyz":  true,
    "/ui":      true,
    "/ui/":     true,   // 前缀：其下所有资源
}
```

`ScopeFor` 把 `/ui/` 当作前缀匿名处理，而不只是那一个精确路径。它背后的处理器只可能返回
内嵌字节与字符串表，因此这个前缀是安全的 —— 换成 `/api/` 前缀就不会是 ——
而 `TestConsoleServesNoEstateData` 正是通过配置一个名字很有辨识度的目标、并要求它不出现在
任何一个控制台响应里，来断言这一点。

`Routes()` 不再是写在 `routes()` 旁边、手工维护的字面量，而改为记录 `routes()` 实际注册了
什么：

```go
func (s *Server) route(pattern string, h http.HandlerFunc) {
    s.registered = append(s.registered, pattern)
    s.mux.HandleFunc(pattern, h)
}
```

这是对特性 009 那条保证的修订，而不是一条新保证。结构性测试 `TestEveryRouteIsGuarded` 遍历的
是一张手写在注册语句旁边的列表；而一条"加了路由却没加进列表"的路由，恰恰对那个专为发现此类
问题而存在的测试是隐形的。现在这张列表不可能与 mux 不一致，而同一个测试也不再自己抄一份
`anonymousRoutes` —— 它直接读包里那一份。

## 5. 字符串表

```go
// consoleStrings 是控制台面向运维的全部词汇表。
// id -> lang -> text。永远两种语言：TestConsoleStringsAreBilingual。
var consoleStrings = map[string][2]string{
    // {en, zh}
    "app.title":       {"MAS-Turbo console", "MAS-Turbo 控制台"},
    "runs.empty":      {"No runs yet.", "尚无诊断记录。"},
    ...
}
```

用二元数组而不是按语言作键的 map，因为这样"配对性"这个问题就没有"某个键缺失"这种答法：
每一项恰好有两个槽位，两个都被检查非空。这与错误码注册表是同一种形状，理由也相同。

`GET /ui/strings.json` 为某一种语言返回 `{"lang": "en", "strings": {"app.title": "…"}}`，
语言由 `?lang=` 选择，默认取服务端配置的语言。阅读者切换语言时控制台再取一次，代价是一个
请求，换来客户端不需要保存这张表。

脚本只通过 `t("id")` 引用字符串。两个测试分别读取脚本与表，并双向比较两个集合：被引用但
不存在的 id 会变成空白标签，存在但没人引用的 id 是死重量，两者都是缺陷。

## 6. 客户端

`app.js` 是单个文件，大致如下：

```
state        {token, lang, strings, route}
t(id)        字符串查表，查不到时回落为 id 本身，好让遗漏可见
api(path)    带 Bearer 头的 fetch；抛出 {code, message, remedy}
el(tag, …)   createElement + textContent；值进入页面的唯一途径
render()     按 location.hash 分发
  #/runs           列表   ← GET /api/v1/diagnoses
  #/runs/<id>      详情   ← GET /api/v1/diagnoses/<id>
  #/runs/<id>/steps 轨迹  ← GET /api/v1/diagnoses/<id>?steps=true
  #/targets        ← GET /api/v1/targets
  #/system         ← GET /api/v1/packs、/topologies、/
```

`el` 就是整个 XSS 防御，浓缩在一个函数里：

```js
function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined && text !== null) n.textContent = String(text);
  return n;
}
```

不存在把值放上页面的第二种方式，因为不存在第二个碰文本的函数，而那些本可以做到这件事的
注入点已由测试确保不存在。

**轮询。** 当详情视图展示的运行状态为 `running` 时，一个 `setTimeout` 每 4 秒重新拉取一次。
路由一变就清除定时器，因此过期视图不会躲在新视图后面继续轮询。

**凭据。** 从 `sessionStorage` 的单个键读写。`api()` 在请求头里发送它。收到 `401` 时控制台
清除它并回到凭据输入界面，同时显示 `MAS-7012` 及其处置建议。没有任何地方把它写进
`localStorage`、`document.cookie` 或任何 URL，并由测试对这三者逐一扫描。

**安全来源横幅。** 若 `location.protocol` 为 `http:` 且主机名不是 `localhost`、`127.0.0.1`
或 `[::1]`，凭据输入框上方会带一条警示。它不拒绝 —— 处在一个把 TLS 终止掉、再在集群内部
以明文转发的代理之后，是一种真实且正确的部署 —— 它只是把自己能看到的事实说出来。

## 7. 诚实地渲染诊断

详情视图是单栏，按此顺序排列，其中没有任何一段是折叠的：

1. **头部** —— id、状态、目标、拓扑、租户（若有）、时间戳。
2. **缺口**（若有），紧跟在头部之后、摘要之前。每一条显示其 `MAS-NNNN` 错误码、想要什么、
   为何失败，以及所声明的影响。它们排在前面，是为了让一个读完摘要就停下来的读者，至少也
   已经看见了它们。
3. **摘要**。
4. **假设**，按排名，每条带百分比形式的置信度与状态词。被否证的仍留在列表里，做标记，不隐藏。
5. **发现**，带严重级别与置信度。
6. **建议**，每条标注"仅供参考"，其标题写明 MAS-Turbo 自身不做任何变更。
7. **证据**，每条带类型、来源与时间窗口。
8. **用量与成本** —— token 数，成本仅在已知时显示；未定价的模型会被点名为"未定价"，
   而不是按零计入。
9. **备注**，以及运行触到上限时的截断警示。

步骤轨迹是一条独立路由而不是一个区块，因为它很长，而想看它的读者知道自己想看它。

## 8. 索引上的 `language`

`GET /` 新增 `"language": cfg.Run.Language`。这是对特性 001 的 LLD §2.16 的一次修订：索引
此前描述了 API，却没有描述服务端自己的呈现选择，而控制台需要它来与运维保持一致，同时不必
再问一遍。

它和索引其余部分一样在 `read` 之后，因此在输入凭据之前控制台先用浏览器的猜测 —— 这本来
也是正确顺序，因为在有东西可问之前，输入凭据的那一屏就得先渲染出来。

## 9. `mas doctor`

一个检查项 `web console`，与 `api exposure`、`tenancy` 并列：

- 已提供，以及在哪个路径 → `CheckOK`
- 被配置关闭 → `CheckOK`，并在详情里说明，而不是给警告：一个刻意做出的配置不是缺陷。

## 10. 错误

| 错误码 | 含义 | 出现位置 |
|---|---|---|
| `MAS-7016` | 当前配置下 Web 控制台已关闭 | `server.ui.enabled: false` 时的 `/ui/…`，HTTP 404 |
| `MAS-7404` | 未找到 | 不在白名单内的资源 |
| `MAS-7012` | 无可用凭据 | 控制台在收到 401 时渲染，附带其处置建议 |

## 11. 测试

| 测试 | 断言 |
|---|---|
| `TestConsoleIsServed` | `/ui/` 返回外壳；`/ui` 重定向到它 |
| `TestConsoleShellIsAnonymousAndDataIsNot` | 资源无凭据即可响应；`/api/v1/*` 仍然拒绝 |
| `TestConsoleServesNoEstateData` | 一个名字很有辨识度的目标不出现在任何控制台响应里 |
| `TestConsoleNeverStartsADiagnosis` | 资源不发出任何 POST，也不引用 diagnose scope |
| `TestConsoleNeverUsesAnHTMLSink` | 资源中不含 `innerHTML`、`outerHTML`、`insertAdjacentHTML`、`document.write`、`eval` 或 `new Function` |
| `TestConsoleSendsAContentSecurityPolicy` | 该响应头存在且默认拒绝；外壳中没有内联 `<script>` 或 `<style>` |
| `TestConsoleServesOnlyItsOwnAssets` | `/ui/` 下未列出的路径返回 `MAS-7404` |
| `TestConsoleStringsAreBilingual` | 每个 id 两种语言齐备且非空 |
| `TestConsoleStringsAreAllUsed` | 被引用集合 ⊆ 表，且表 ⊆ 被引用集合 |
| `TestConsoleRendersTheErrorCode` | 资源从失败响应中读取 `code`、`message` 与 `remedy` |
| `TestConsoleKeepsTheCredentialOutOfURLsAndCookies` | 无 `localStorage`、无 `document.cookie`、查询串中无凭据 |
| `TestConsoleSurfacesGapsAndAdvisoryStatus` | 资源引用了 gaps、advisory、unpriced 与 truncated |
| `TestConsoleCanBeDisabled` | `enabled: false` 返回 `MAS-7016`；默认值提供服务 |
| `TestIndexReportsTheLanguage` | `/` 携带 `language` |
| `TestDoctorReportsTheConsole` | doctor 说明控制台状态 |

### 浏览器发现了它们发现不了的东西

本特性发布之前，控制台被放进一个无头浏览器里，对着一个真实运行的 `mas serve` 跑过一遍 ——
这不是构建会执行的检查，而正是那些结构性测试明说自己做不到的那种验证。它找出了一个 Go
测试看不见的缺陷：

**首次读取时凭据被拒，留给用户的是一个空白输入框。** `boot()` 会读取索引以得知服务端配置的
语言。当 `sessionStorage` 里存着一个过期令牌时，这次读取返回 `401`，`api()` 随即忘掉该凭据，
而那个 `catch` 以"下面的 route 会把它显示出来"为由把失败吞掉了。它显示不出来：`route()`
随后发现没有令牌，于是渲染凭据输入界面 —— 而它无话可说。读者看到的是一个空白输入框，
本该出现在那里的是 `MAS-7012` 及其处置建议，而这恰恰是 FR-010 存在的目的。

修复方式是 `state.problem`：在任何视图存在之前抛出的失败先存放在这里，`viewGate` 把它渲染在
输入框上方，然后清空。这也正是为什么 FR-010 的保证如今在最需要它的那条路径上得到了兑现 ——
在那条路径上，读者没有别的办法知道出了什么问题。

另外两处较小的改进，来自阅读渲染出来的页面而不是代码：

- `withoutCode(detail, code)`：当错误码已经渲染在上方时，从缺口或步骤的 detail 里去掉开头的
  `MAS-NNNN: `。`Gap.Detail` 通常就是 `err.Error()`，于是那一行会把自己的错误码说两遍。
- `counted(title, list)`：把数量放进区块标题。没有任何东西被折叠 —— 设计禁止折叠 —— 但一次
  未配置遥测的运行产生了二十七个缺口，读者理应在开始往下翻之前就知道这一点。

### 这些测试证明不了什么

十五个里有九个是**结构性**测试：它们读取内嵌资源，断言某个构造存在或不存在。它们无法证明
控制台渲染正确，因为要证明那一点需要一个浏览器，而为一个特性引入浏览器依赖，是本仓库不打算
付出的代价。

扫描能判定的部分，它判定得很彻底：某个危险注入点是否出现在文件中的任何位置，是一个有确定
答案的文本问题；字符串 id 是否两两对上，也是。所选的正是这些不变量，选它们是因为它们是那些
会**无声**失败的：布局坏了一眼就能看见，而存储型 XSS 注入点不会。

行为的那一半在行为所在之处被覆盖：控制台所读取的 API 有直接而充分的测试，而 `make demo`
会经由 Markdown 路径渲染同一份报告。

## 变更记录

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.1 | 2026-08-27 | §11：记录一次一次性无头浏览器运行的发现 —— boot 阶段凭据被拒时渲染出空白输入框而不是 `MAS-7012`，已用 `state.problem` 修复 —— 另有 `withoutCode` 与 `counted` | 代码 |
| 1.0.0 | 2026-08-27 | 初始详细设计 | tasks、代码 |
