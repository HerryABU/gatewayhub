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

## 4.1 SPA 类服务接入指南（openlist / alist / 各类 Vite SPA）

这类服务的前端会在**运行时**根据自己生成的 `base_path` 拼接资源与 API 地址。实测（openlist v4.2.10）：

- `base_path` 由后端配置 `site_url` 的路径部分决定，`site_url` 为空时是 `/`；
- 于是浏览器会去请求 **网关根路径** 的 `/assets/*.js`、`/api/public/settings` —— 这些路径没有服务前缀，
  网关按 FR-3 一律 404（不猜测归属），页面上表现为「白屏 / 获取设置失败」。

**唯一正确的接法：让后端自己认前缀，网关不再剥离。**

1. 后端配置子路径（以 openlist 为例，`data/config.json`，改完需重启）：

   ```json
   { "site_url": "http://<代理域名>/openlist" }
   ```

   之后 openlist 自己就会把 `base_path` 变成 `/openlist`，`/` 也会 302 到 `/openlist`，
   页面里的资源、`import()` 动态 chunk、API 全部自带前缀。

2. 网关配置表里该服务必须是 **`pass-through`**（原样透传前缀），并且**不要**开 `html_rewrite`：

   ```yaml
   services:
     - name: openlist
       target: 127.0.0.1:5244
       mode: pass-through
       cookie_hat: "openlist__"
       known_abs_prefixes: ["/assets", "/api"]   # 仅用于越界归因告警
   ```

3. 反过来，**strip + 后端自带 base path 是死局**：网关把 `/openlist` 剥成 `/`，后端又把 `/` 302 回
   `/openlist`，循环闭合。网关会这样处理：

   - 第一次命中：记一条 `redirect_loop` 诊断（明确写出「该服务疑似已配置 base path，应改 pass-through」），照常放行；
   - 同一 服务+来源+后端路径 在 60 秒内第二次命中：判定为确定性死循环，返回 **508** 并附修复建议，
     不再让浏览器转到 `ERR_TOO_MANY_REDIRECTS`。

   判据是纯函数式的：`Location` 去掉服务前缀后与本次转发给后端的路径完全相同 —— 不可能前进。
   一次性自跳（302 到同一路径 + 种 Cookie）不受影响，因为它不会连续命中。

## 5. 已知限制（代理层不承诺解决）

1. **JS 运行时拼接的根路径**：`fetch('/api')`、`import('/chunk.js')`、模板字符串拼 URL —— 不改写。
2. **SPA 未配置 `basename`/`base`**：运行时路由（history.pushState + 直接刷新深层链接）可能跳出前缀，需要应用层配置。
3. **硬编码自身 `IP:端口` 的后端**：响应体里的绝对地址不改写（只改 `Location` 头中指向后端的绝对 URL）。
4. **带 bug 的后端**：异常重定向、非法 Cookie、路径混乱 —— 代理只做隔离、记录、降级。
5. **浏览器已跳出 `/{服务名}/` 后的请求归属**：不推断，直接 404（NFR-5 不猜测）。

对上述情况，代理只保证三件事：**请求落在 `/{服务名}/` 内、Cookie 不跨服务污染、重定向不跳出前缀**。

---

## 6. 兼容性标记

配置里用 `no_full_compat: true` 显式声明「不承诺完整兼容」，便于团队对齐期望。

| 后端类型 | 推荐模式 | 建议开关 | 标记 |
|---|---|---|---|
| Go / Node 普通 Web（模板渲染） | `strip` | `html_rewrite: true` | — |
| 静态站 / 纯 HTML | `strip` | `html_rewrite: true` | — |
| Java（自带 context-path） | `pass-through` | `html_rewrite: false` | — |
| SPA（未配 basename） | `strip` | `html_rewrite: true` | `no_full_compat: true` |
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
