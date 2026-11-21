# 反向代理路径隔离网关 · 部署与运维说明

> 本模块是 GatewayHub 的「路径隔离」转发层：按路径第一段 `/{服务名}/` 路由到后端，
> 每个请求执行一次 **拦截 → 修正 → 转发 → 响应修正**，处理完即结束（无跨请求状态）。
> 配置表在 `isolation.yaml`（显式声明），代码只实现规则、不认识任何具体服务。

---

## 1. 处理模型（单循环、确定性）

| 阶段 | 做什么 | 关键约束 |
|---|---|---|
| 拦截 | 取路径第一段匹配服务名 | 未命中 **404**，不猜测归属 |
| 请求修正 | 路径剥离/保留、Host 策略、注入 `X-Forwarded-*`、Cookie 脱帽 | 每个请求只修正一次 |
| 转发 | 单次转发到 `target` | 绝不因响应内容二次发起请求 |
| 响应修正 | `Location`、`Set-Cookie`、可选 HTML/CSS 字面量 | 只修正一次，不形成代理内部回环 |

三种模式（`mode`）：

| 模式 | 路径 | 响应体 | 适用 |
|---|---|---|---|
| `pass-through` | 原样传 `/{服务名}/...` | 永不改 | 后端自己认前缀（Java context-path 等） |
| `strip` | 剥离前缀，后端看到 `/...` | 可按开关改字面量 | 普通 Go/Node/静态站 |
| `isolated` | 剥离前缀 | **永不改**（仅头层） | 出问题时的降级档位 |

> `isolated` 是「安全档」：所有改写开关在此模式下自动失效（代码层面强制，不靠配置自觉）。

---

## 2. 部署

### 2.1 开关（config.yaml）

```yaml
isolation:
  enabled: true            # 关掉则完全走原有 DB 路由逻辑
  file: isolation.yaml     # 服务配置表
  watch: true              # 文件变更热加载（约 5s）
  watch_interval: 5        # 轮询间隔（秒）
  audit_file: logs/isolation-audit.log   # 变更审计（JSONL）
  trust_proxies: false     # 网关前面还有可信反代才设为 true（决定客户端 IP 取值）
```

`isolation.yaml` 缺失或格式错误时：**记录 config_error 并保持未启用**，不影响网关控制台与其他路由。

### 2.2 路由优先级

```
/api/*（网关控制台）                  ← 最高，服务名禁止占用 api/assets/static/favicon/health/isolation/login
/{服务名}/...（isolation.yaml 声明）   ← 优先于 DB 路由
DB 路由（控制台「路由管理」）           ← 原有能力，未声明进 isolation.yaml 的服务继续走这里
SPA 前端回退
```

> 注意：一旦 `isolation.yaml` 声明了至少一个服务，网关就视为「路径空间已显式声明」，
> 不再启用原有的「子路径部署前缀猜测」——未声明的多段路径直接 404（NFR-5 不猜测归属）。
> 若网关部署在 `https://example.com/gw/` 之后，请在反代侧剥离 `/gw`。
> 未声明任何服务时，原有行为完全不变。

### 2.3 端口与日志

- 监听端口：`server.port`（默认 `8088`），与网关同一进程、同一端口。
- 业务访问日志：`access_log.dir`（既有能力，按天/小时落盘）。
- 隔离网关审计：`isolation.audit_file`，JSON Lines，每次 reload / 模式切换一条。
- 诊断日志：进程 stdout，统一前缀 `[isolation][分类][服务名]`。

---

## 3. 运维接口（需管理员 JWT）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/isolation/services` | 每个服务的配置 + 熔断状态 + 指标（FR-19） |
| GET | `/api/isolation/metrics` | 纯指标快照（便于外部监控抓取） |
| GET | `/api/isolation/audit?limit=50` | 最近变更审计 |
| PATCH | `/api/isolation/services/:name/mode` | 一键切模式，body `{"mode":"isolated"}` |
| POST | `/api/isolation/reload` | 立即重新加载 `isolation.yaml`（失败保留原配置） |

一键降级示例：

```bash
curl -X PATCH http://127.0.0.1:8088/api/isolation/services/SPA/mode \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"mode":"isolated"}'
```

### 3.1 控制台页面（无需 curl）

管理员控制台左侧「🧩 路径隔离」页把上述接口做成可视面板：

- **概览卡**：服务数、总请求量、改写总次数（Location/Cookie/HTML 分项）、熔断中的服务数。
- **服务表**：每个服务的后端、`处理模式`（下拉即改，切 `isolated` 需二次确认）、Cookie 帽子、熔断状态、请求量、5xx 比例、平均耗时；行首可展开看 13 项细分指标、Host 策略、超时、已知绝对路径前缀。
- **降级按钮**：一行一个，等价于 `PATCH .../mode`，点击即生效并写审计。
- **配置告警**：`isolation.yaml` 校验产生的 Warnings（如 `html_rewrite` 在非 strip 模式无效、帽子互为前缀）在页面顶部常驻提示。
- **变更审计**：reload / 模式切换的时间、操作者（JWT 用户名）、服务、`from → to`。
- 服务表每 5 秒自动刷新，可关闭；`重新加载配置` 按钮用于改完 `isolation.yaml` 后立即生效（文件监听本身也会热加载）。

> 页面只是这些接口的视图，**不承担任何网关逻辑**：所有改写、熔断、隔离行为仍在 Go 侧，关掉前端不影响代理功能。

![路径隔离控制台](img/isolation-console.png)

---

## 4. 指标与告警口径

按服务统计（`GET /api/isolation/metrics`）：

| 指标 | 含义 | 排障用法 |
|---|---|---|
| `requests` / `status_4xx` / `status_5xx` | 请求量与错误比例 | 5xx 高 → 后端问题；4xx 高 → 路由/鉴权语义问题 |
| `backend_errors` | **代理**发不出请求（连接拒绝/超时） | 与 5xx 区分：这类是「后端不可达」 |
| `location_rewrites` | `Location` 改写次数 | 突增往往意味着后端在换重定向策略 |
| `cookie_rewrites` / `cookies_dropped` | 加帽/丢弃次数 | 登录态异常时先看这两项 |
| `html_hits` / `html_failures` / `body_skipped` | 字面量改写命中/失败/跳过 | 失败多为 gzip/br 编码或体积超限 |
| `breaker_trips` | 熔断触发次数 | 结合 `breaker.state` 判断是否自动降级 |
| `loop_alerts` | 重定向循环告警次数 | 通常对应后端 bug，或后端自带 base path 却用了 strip |
| `abs_prefix_hits` | **越界归因**次数：请求未带服务前缀、但命中该服务声明的 `known_abs_prefixes`（已 404） | >0 基本等于「这个后端没配 base path」，见 §4.1 |

日志分类（FR-18，责任边界一眼可辨）：

| 分类 | 责任方 | 典型内容 |
|---|---|---|
| `backend_bug` | 后端 | 非法 `Set-Cookie`、连接拒绝、超时 |
| `rewrite_failed` | 代理 | gzip 解压失败、体积超限、编码不支持 |
| `config_error` | 配置 | YAML 解析失败、字段非法、热加载失败、`known_abs_prefixes` 越界归因、配置导致的重定向自环 |
| `proxy_degrade` | 代理（主动） | 熔断打开、服务被禁用 |
| `redirect_loop` | 后端（多为 bug） | 同一来源短时间反复 3xx |
| `rewrite_ok` | 代理（正常） | 命中数与响应大小 |

---

## 4.1 服务接入三级方案（路径前缀 / 后端自认 / 子域名）

不同后端「知不知道自己被挂在子路径上」差别极大，按这个顺序选方案：

### A. 路径前缀模式（默认推荐）：`strip` + `html_rewrite: true`

网关在**转发的那一份 HTML** 上做四层准备，后端文件与配置一个字节都不动
（等价于 nginx `sub_filter` / Apache `mod_proxy_html`，但只认挂载点声明，不做全文替换）：

1. **静态改写**：HTML 的 `href/src/action` 与 CSS 的 `url()` 补前缀；`data-src` 这类
   「延迟地址」故意不改（它会被前端再拼一次基准，见第 4 点）；**JS 字面量一律不碰**
   （只改整页导航 `location.href|assign|replace`）。原因见下面的「为什么必须分层」。
2. **挂载点声明改写**：把内联运行期配置里值为 `"/"` 的挂载点改成 `/{服务名}` ——
   匹配 `base_path` / `basePath` / `basename` / `baseURL` / `publicPath` / `asset_prefix` / `router_base`，
   `key: "/"` 与 `"key":"/"` 都认。SPA 的路由 basename、axios baseURL、运行时资源前缀都从这里取值，
   这是「一个后端不知道自己被挂在子路径」时最根本的一处。
3. **资源基准一律不伪造**：`vite-plugin-dynamic-base` 的 `window.__dynamic_base__`（openlist / alist 在用）
   属于应用运行态，网关**不碰**。曾给它做「空值兜底」并因此把 openlist 首屏打挂，详见 §5.1。
   prefetch / modulepreload 的地址由第 4 步的属性 setter 在赋值时补前缀即可。
4. **运行期请求 shim**：`<head>` 最前面注入脚本，给真正**发出去的请求**补前缀 ——
   `fetch`、`XMLHttpRequest`（axios 走它）、`sendBeacon`、`EventSource`、`WebSocket`、
   `history.pushState/replaceState`，以及 DOM 的 `src`/`href`（`setAttribute` + 属性 setter）。
   入参先剥同源绝对 URL（`http://gw/assets/x.css` → `/assets/x.css`）再补前缀：vite 的
   `__vitePreload` 用 `new URL(dep, import.meta.url)` 会把根相对 dep 规约成绝对地址，
   不剥就会全部打到网关根。跨域 CDN / 图床原样放行。

为什么必须分层：**JS 里以 `/` 开头的字符串既可能是 URL，也可能是请求体数据，文本层无法判断**。
已经真实踩中两次：

- NVS：`/api/batch` 的请求体是 `{id,method,path}`，`path` 值形如 `/api/nav-layout`。
  改成 `./api/nav-layout` 后后端回 `path 必须以 /api/ 开头`，11 个子请求全失败，顶部导航整条消失；
- openlist：接口写成 `` $.get(`/public/plugins`) ``，而 `/public` 很像静态资源目录。
  加了前缀后与 axios 的 `baseURL` 叠加成 `/openlist/api/openlist/public/plugins`，
  所有接口拿到 index.html（模块 MIME 报错、设置页报 `获取设置失败`）。

所以结论是**文本层不猜**：静态层只改 HTML 属性、CSS 与整页导航；API 与运行时资源地址
一律交给 shim 在请求发出时补，只碰请求、不碰数据。

隔离引擎（`strip` + `html_rewrite`）与数据库路径路由（`/前缀/*`）用的是同一套准备逻辑，行为一致。

实测 openlist（`site_url` 留空、`data/config.json` 与备份 md5 完全一致）、Chromium 真机
`http://127.0.0.1:8088/openlist/`：登录页完整渲染（标题「登录到 OpenList」、账号/密码/记住账号/
忘记密码/清除/游客浏览全部就位），表单提交打 `/openlist/api/auth/login/hash` 并回
`Invalid username or password` 业务文案，`styles` 全部落在 `/openlist/assets/*`，
**0 个 4xx、0 个 pageerror**。懒加载 chunk（`entry-*.js`、`login-*.js`、`store-*.js`、`zh-CN-*.js`）
与 preload CSS 全部命中 `/openlist/assets/*`。

> 修复前这里曾是"假绿"：curl 看首页 200，真机却报 `System error: Unable to preload CSS`，
> 30+ 个 chunk 打到 `/assets/*` 404。原因是 `__vitePreload` 用 `new URL()` 把根相对 dep
> 规约成了同源绝对地址，而 shim 只处理以 `/` 开头的字符串。
> **验收必须看前端，不能看状态码**。

### B. 后端自认前缀：`pass-through`

适合本来就有「站点子路径」配置项的应用（如 Java 的 `context-path`）：后端自己输出带前缀的地址，
网关原样透传前缀、不改响应体，代价是**要改后端配置**：

```yaml
services:
  - name: java-app
    target: 127.0.0.1:8080
    mode: pass-through
    cookie_hat: "javaapp__"
```

注意这是**应用侧声明**，不是网关能力；能用 A 或 C 就不要选 B（A 不需要动后端）。

⚠️ `strip` + 后端自带 base path 是死局：网关把 `/openlist` 剥成 `/`，后端又把 `/` 302 回 `/openlist`。
网关第一次只记 `redirect_loop` 诊断并放行，60 秒内第二次判定确定性死循环 → **508** 并附修复建议。
判据是纯函数式的：`Location` 去掉服务前缀后与本次转发给后端的路径完全相同。

### C. 子域名模式：零改写、零后端配置（真·透明反代）

访问 `http://{转发名}.{base_domain}:{端口}/`，路径就是后端根路径，**响应体一个字节都不改**：

- 路由表里的 `{转发名}` 直接作为子域名生效，无需额外配置；
- 后端 `site_url` 保持空、`base_path` 保持 `/` 即可，前端运行时拼出来的 `/assets/*`、`/api/*`
  天然落在同一个 origin 内，不存在前缀问题；
- 网关自己的 `/api`、`/assets` 守卫与部署前缀剥离都会**让位**给子域名路由（否则后端 API 会被网关吞成 404）。

实测 openlist v4.2.10（`site_url` 为空）：`http://openlist.localhost:8088/` 首页 → 302 `@login`，
0 个 4xx、0 个 JS 错误。A 与 C 都能做到「不碰后端」，差别在于：

- **A**：同一个域名、同一端口，多个服务共用一个 origin（适合「一个入口挂多个系统」）；
- **C**：每个服务独占一个子域名，响应体零改写（速度=源站，排障最简单），需要 DNS/泛解析或 `*.localhost`。

## 5. 已知限制（代理层不承诺解决）

1. **构建期硬编码的绝对基址**：打包产物里如果直接把 `base: "/cdn-x/"`（或 `import "/cdn-x/a.js"`）
   编译进模块图，且应用没有读取任何运行期挂载点配置，那么网关无从得知该改哪个值 ——
   这种情况只能改构建配置，或用 §4.1 的 C 方案（子域名下根路径即本服务的根）。
   常见框架的**运行期**挂载点（`base_path`/`basename`/`baseURL`/`publicPath`/`asset_prefix`/`router_base`）
   会按键名改写（只改值为 `"/"` 的声明）。`__dynamic_base__` 属于**应用运行态**，
   一律不碰 —— 伪造它会改变应用的控制流分支（见 §5.1）。
2. **SPA 的路由 basename**：`<base>` + 挂载点声明 + shim 的 `pushState` 补丁能覆盖多数情况；
   应用若把 basename 硬编码在路由表里，请改用运行期配置读取。
3. **硬编码自身 `IP:端口` 的后端**：响应体里的绝对地址不改写（只改 `Location` 头中指向后端的绝对 URL）。
   注意 shim 只剥 `location.origin` 后补前缀，跨域 CDN / 图床原样放行。
4. **带 bug 的后端**：异常重定向、非法 Cookie、路径混乱 —— 代理只做隔离、记录、降级。
5. **浏览器已跳出 `/{服务名}/` 后的请求归属**：不推断，直接 404（NFR-5 不猜测）。

### 5.1 铁律：不伪造应用运行态

路径式反代允许改**响应的字节**（补前缀），但不允许改**应用的运行态**。
两者差别在于后者会改变应用自己的控制流：

> openlist 的 `index.html` 里写的是 `if (window.__dynamic_base__) { 走 CDN 分支 }`。
> 网关曾给它做「空值兜底」（留空就返回网关前缀），于是应用误判「配了 CDN」，
> 拼出 `__dynamic_base__ + item.attrs.src`；而 preloads 里恰好有一条 `script`
> 只有 `data-src`、没有 `src`，结果得到 `/openlistundefined`，DOM 上凭空多一个 404 脚本。

同理，**不要**去改 `window.__POWERED_BY_QIANKUN__`、`__NUXT__`、`__NEXT_DATA__`、
`__INITIAL_STATE__`、`window.process.env` 这类「应用用来判断自身环境」的变量。
补前缀只应发生在**请求发出**与**属性赋值**这两个时刻。


对上述情况，代理只保证三件事：**请求落在 `/{服务名}/` 内、Cookie 不跨服务污染、重定向不跳出前缀**。

---

## 6. 兼容性标记

配置里用 `no_full_compat: true` 显式声明「不承诺完整兼容」，便于团队对齐期望。

| 后端类型 | 推荐模式 | 建议开关 | 标记 |
|---|---|---|---|
| Go / Node 普通 Web（模板渲染） | `strip` | `html_rewrite: true` | — |
| 静态站 / 纯 HTML | `strip` | `html_rewrite: true` | — |
| 常规 SPA（Vue/React 打包，URL 以字面量出现） | `strip` | `html_rewrite: true` | — |
| 运行时按 `location` 拼绝对路径的 SPA（openlist/alist） | `strip` | `html_rewrite: true` | — |
| Java（自带 context-path） | `pass-through` | `html_rewrite: false` | — |
| 老旧系统（重定向/ Cookie 有 bug） | `isolated` | 全关 | `no_full_compat: true` |
| WebSocket / SSE 服务 | 任意 | 按需 | 已透传 Upgrade 与 Flush |

---

## 7. 验收对照

| 需求验收项 | 对应自动化测试（`internal/isolation/engine`） |
|---|---|
| 1. `/A/admin` 正确路由、后端收到符合模式的路径 | `TestAcceptanceRoutingAndHeaders` / `TestRootPathJoin` |
| 2. `Location: /home` → 浏览器落在 `/A/home` | `TestAcceptanceLocationRewrite` |
| 3. A 的 Cookie 不被 `/B/` 误用、`Path` 已收紧 | `TestAcceptanceCookieIsolation` |
| 4. 未匹配 → 404，不转发 | `TestAcceptanceRoutingAndHeaders` |
| 5. 开了开关才补前缀，关掉不改 body | `TestAcceptanceHTMLRewriteSwitch` / `TestBodyRewriteSizeLimit` |
| 6. 指标可查询、能区分改写命中与后端错误 | `TestMetricsFailuresDistinguished` / `TestSnapshotShape` |
| 7. 切 `isolated` 后只改头层、其他服务不受影响 | `TestAcceptanceIsolatedDegradation` |
| 8. 后端连续失败熔断，其他服务正常 | `TestAcceptanceCircuitBreaker` |

附加覆盖：`TestSingleForwardPerRequest`（只转发一次）、`TestReloadHotSwap`（热加载）、
`TestRedirectLoopAlert`（循环告警）、`TestHostModeRewrite`（Host 策略）、`TestGzipBodyRewrite`（gzip 改写）。
