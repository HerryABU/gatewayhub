# GatewayHub 多应用聚合网关

[![CI](https://github.com/HerryABU/gatewayhub/actions/workflows/ci.yml/badge.svg)](https://github.com/HerryABU/gatewayhub/actions/workflows/ci.yml)
[![Release](https://github.com/HerryABU/gatewayhub/actions/workflows/release.yml/badge.svg)](https://github.com/HerryABU/gatewayhub/actions/workflows/release.yml)

轻量级、带管理界面、支持动态配置的统一接入网关。所有后端服务通过**一个端口**对外暴露，路径格式 `/{转发名}/...`，路由规则可实时增删改、无需重启。

> 开源协议：GNU AGPL-3.0 · 后端 Go 1.21+ · 前端 Vue 3 + Vite + Element Plus + ECharts · **v1.7.0**

---

## ✨ 功能特性

### 核心转发
- 基于路径前缀的反向代理（`net/http/httputil.ReverseProxy`）
- **子域名路由**：`java-order.localhost:8088/user` 与 `/{prefix}/user` 双模式并存，子域名命中时**零路径重写、零 `<base>` 注入**，纯透明转发（速度=源站）；子域名优先级最高——连后端自己的 `/api/*`、`/assets/*` 也直接转发，不会被网关同名路径吞掉
- **多级路径前缀**（最长前缀优先）：`v2/beta` 可覆盖 `v2`，段边界对齐
- **WebSocket** 透明转发（HMR / 推送 / 聊天长连接不超时）
- 后端地址支持 `:8080`、`:8080/api/v1`、完整 URL（**含自定义端口**，如 `http://example.com:8443`）及 **`${ENV}` 环境变量**（如 `http://${ORDER_HOST}:8080`，未设置时创建路由报错）
- **外部网站代理**：完整 URL 目标天然支持（含 HTTPS），健康检查同样按 scheme/host 探测
- **响应内容「绝对改相对」重写**：HTML 属性/内联样式根绝对路径 → 文档相对路径（`./x`），并注入 `<base href="/{prefix}/">` 兜底解析；外链 CSS 的 `url()/@import` 按 CSS 文件自身位置换算相对路径；JS **只改整页导航**（`location.href|assign|replace`），其余字面量一律不动——文本层判不出「URL 还是请求体数据」，实测已踩中两次（NVS 的 `{path:"/api/x"}`、openlist 的 `$.get("/public/plugins")`），这类地址全部交给运行期 shim 在发请求时补前缀
- **运行期挂载点改写**：内联配置里值为 `"/"` 的挂载点（`base_path` / `basename` / `baseURL` / `publicPath` / `router_base`）改为 `/{prefix}`；**刻意不碰 `__dynamic_base__`**（那是应用运行态，伪造会让 openlist 走进 CDN 分支拼出 `/openlistundefined`）。SPA 的路由 basename、axios baseURL、Vite preload 的 chunk 基址都取自这里——**后端零配置就能挂到子路径**（实测 openlist v4.2.10：登录页与 6 个 `/openlist/api/*` 全 200，0 个 4xx）
- **运行期前缀 shim**：`<head>` 最前注入一段脚本，给真正**发出去的请求**补前缀 —— `fetch` / `XMLHttpRequest`（axios 走它）/ `sendBeacon` / `EventSource` / `WebSocket` / `history.pushState|replaceState`，以及 DOM 的 `src`/`href`（`setAttribute` + 属性 setter）。JS 里的 `/api/*` 字符串**不做静态改写**（它可能是 `{path:"/api/x"}` 这类请求体数据，改了会污染业务），只碰请求不碰数据
- 路由**毫秒级热加载**，无需重启
- 透传请求头，自动添加 `X-Forwarded-For` / `X-Real-IP` / `X-Forwarded-Proto`
- 超时/错误处理：502 / 504 / 404 / 503

### 访客门户与友链
- **门户式首页**：访客看到的是服务卡片 + 友链卡片 + 服务总览；Hero 不再出现「管理员登录 / 进入控制台」按钮，**后台入口收敛到右上角一个图标**（悬停提示），已登录则是用户菜单
- **友链是纯跳转**：`GET /api/friend-links` 公开可读，`POST/PUT/DELETE` 需管理员 JWT；它们**不创建反向代理路由**、不参与前缀匹配，只是 `<a target="_blank" rel="noopener noreferrer">`
- 默认内置一条 **GatewayHub 项目主页**友链，可在控制台「🔗 友链管理」里增删改；只接受 `http(s)://` 绝对地址（`javascript:` / `data:` 会被拒绝与过滤）
- 访客态看不到后端地址列（仅管理员可见）

### 路径隔离网关（显式配置 · 可降级）
- **首段路由**：`/{服务名}/...` 精确匹配 `isolation.yaml` 中声明的服务，未命中直接 404（不猜测归属）
- **三种模式**：`pass-through`（后端自己认前缀）/ `strip`（剥离前缀，可改 HTML-CSS 字面量）/ `isolated`（仅头层修正，**响应体一个字节都不改**）
- **Cookie 帽子**：`A__session` 按服务加前缀，响应侧 `Path=/` 收紧为 `Path=/A`，多值逐条处理且不丢 `HttpOnly/Secure/SameSite`
- **响应修正**：`Location` 按模式补前缀并屏蔽指向后端的绝对 URL；`Set-Cookie` 加帽；HTML/CSS 只改 `href=/src=/action=/url(` 这类带上下文字面量
- **可控降级**：单服务连续失败自动熔断（其他服务不受影响），一条 API 即可把服务切到 `isolated`
- **越界归因**：后端前端没配 base path 时打到根路径的 `/assets/*`、`/api/*` 会被识别并归因到具体服务（`abs_prefix_hits`），不再只是一条无意义的 404
- **自环守卫**：strip 模式下后端自带 base path 造成的 302 死循环，第二次命中直接返回 508 + 修复建议，不让浏览器转到 `ERR_TOO_MANY_REDIRECTS`
- **可观测**：按服务统计请求/4xx-5xx/改写命中/改写失败/熔断/重定向循环，日志按「后端 bug / 代理改写失败 / 配置错误」分类
- **控制台可视化**：左侧「🧩 路径隔离」页可看每个服务的配置与实时指标、下拉切模式一键降级、查看配置告警与变更审计，改完 `isolation.yaml` 点一下即热加载
- 详见 [docs/isolation-gateway.md](docs/isolation-gateway.md)

### 健康检查（绿 / 橙 / 红状态条）
- 每 30 秒主动探测各下级站点的 `/health` 端点（TCP 兜底）
- 测量响应延迟：🟢 健康 / 🟠 降级（缓慢）/ 🔴 宕机 / ⚪ 未知
- 后台「健康检查」页以发光状态条实时展示，支持手动立即检查

### 访问统计与地理位置
- 异步日志写入（Channel + Worker Pool 批量入库），不阻塞转发
- 总 PV / 今日 PV / 趋势图 / 路由 PV 明细（含 Sparkline）
- **访问者地理位置**：ip2region 离线库解析 IP → 国家/省/市
- **中国地图**（省级热力图）+ **世界地图**（国家级热力图），一键切换
- 地图边界符合国家测绘标准（含台湾、南海诸岛）
- **访问日志落盘（含完整请求头）**：按 天/小时 组织 `logs/YYYY-MM-DD/HH.log`，每行一条 JSON

### 管理控制台（现代化侧边导航）
- **访客页**：Hero 高级感横幅 + 开放服务竖排列行（UptimeBar 条条 + hover 浮动发光）+ 站点介绍
- **控制台**：现代侧边导航布局，8 大功能模块（路由/健康/统计/地图/安全/迁移/备份/合规）
- **站点介绍**：站点级简介（访客页横幅）+ **各挂载站介绍**（路由 description，访客页逐站展示）
- **管理员改密码**：用户菜单一键修改（原密码校验 + 长度/一致性校验）
- 中英文双语，主题浅色/深色一键切换

### 安全防护（DDoS / CC / WAF）
- **DDoS/CC 限流**：每 IP 令牌桶限流 + 连续超限自动封禁
- **SQL/XSS 拦截（WAF）**：对解码后的 URL、查询参数、请求体做注入/跨站模式检测
- **IP 黑白名单**：支持单 IP 与 CIDR（如 `1.2.3.0/24`）
- **API 黑白名单**：对 `/api/*` 路径做前缀匹配放行/拦截
- 全部可在后台「安全防护」页管理，规则实时生效

### 建站向导（一次性，严防死守）
- 首次运行自动进入建站向导（站点名 / 语言 / 管理员账号 / 数据库配置）
- 完成后**永久禁用**，入口不可二次进入；未完成时仅放行向导相关路径
- 向导默认**仅允许 localhost 访问**（`setup.allow_remote` 可配置），防止他人抢占

### 数据库迁移与备份
- **数据库迁移**：SQLite ⇄ MySQL、MySQL → MySQL，一键复制全部表与数据
- **多数据库备份**：手动备份 + 定时备份（默认每日），自动清理旧备份
- SQLite 使用 `VACUUM INTO` 安全备份，MySQL 生成逻辑 SQL 导出

### 合规（《网络安全法》2025 修正版）
- 说明：该法 2025 年修正后**共 81 条，不存在第 176 条**
- 已落地最相关条款：第 23 条（网络日志留存≥6 个月，默认 180 天）、第 42/43/44 条（个人信息保护）
- 后台「合规说明」页展示条款原文与落地对照

### 国际化（i18n）
- 前端内置 **简体中文 / English** 双语言，顶栏一键切换，偏好持久化

---

## 🚀 快速开始

### 直接运行（推荐）

```powershell
# 1. 一键编译（前端 + 全平台二进制，产物在 release/）
powershell -ExecutionPolicy Bypass -File build-all.ps1

# 2. 或仅编译当前平台
powershell -ExecutionPolicy Bypass -File build-all.ps1 -Platforms windows

# 3. 手动编译单个
go build -o gatewayhub .
```

```powershell
# 一键构建脚本参数
.\build-all.ps1                      # 前端 + 全平台（win/linux/mac × 10 目标）
.\build-all.ps1 -Platforms windows    # 仅 Windows（amd64/arm64）
.\build-all.ps1 -SkipFrontend         # 跳过前端构建（复用现有 web/dist）
```

> 脚本自动：检测 go/npm/IP 库 → `npm run build` → 交叉编译（CGO_ENABLED=0，
> 内嵌前端与 IP 库）→ 可选 UPX 压缩 → 汇总。旧产物自动备份为 `release.old/`。
> 说明：数据库驱动 glebarez/sqlite（modernc 纯 Go 移植）不支持
> windows/386、windows/arm、linux/mips64，已从目标列表排除。

浏览器打开 `http://localhost:8088`，首次运行会进入**建站向导**，按步骤配置站点名、管理员账号、数据库即可。

> 重新体验建站向导：删除 `data/gateway.db` 后重启即可。

#### 两种访问方式

| 方式 | 示例 | 说明 |
| :--- | :--- | :--- |
| 路径前缀 | `http://localhost:8088/java-order/user` | 剥离 `/java-order` 转发，响应内容自动重写 |
| 子域名 | `http://java-order.localhost:8088/user` | Host 头命中，零重写纯透明（更快） |

> 子域名默认基于 `localhost`（现代浏览器会直接把 `*.localhost` 解析到 `127.0.0.1`，无需改 hosts）。可经 `server.base_domain` 换成自己的域名。

### 子路径部署（自建反向代理 `/{name}/` → 网关）

网关自身可挂在任意反向代理子路径下，前缀由反代决定、**严禁硬编码**（`{name}` 仅为示例）：

- **前端**：构建产物全部使用相对路径（`base: './'`），运行时通过 `import.meta.url` 动态推导部署根，API 请求、前端路由、业务链接自动带上当前前缀；无前缀部署时自动退化为 `/`。
- **后端**：请求进入时「智能剥离」首段部署前缀——仅当首段既非业务路由（含多级前缀首段）、亦非保留字（`api/assets/static/favicon`）且路径含第二段时才剥离，因此网关 UI、API、静态资源以及**嵌套的业务路由**（`/{name}/java-order/...`）在任意帽子下都能工作。

```nginx
# nginx 示例：https://example.com/gw/ → http://127.0.0.1:8088/（剥离前缀）
location /gw/ {
    proxy_pass http://127.0.0.1:8088/;          # 末尾 / 表示剥离 /gw 前缀
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;     # WebSocket
    proxy_set_header Connection "upgrade";
}
```

也可配置**透传前缀**（`proxy_pass http://127.0.0.1:8088;` 不带末尾 `/`），网关会自动识别并剥离首段部署前缀。

### 开发模式

```bash
# 后端
go run main.go -config=config.yaml      # 监听 :8088

# 前端（热更新）
cd web && npm install && npm run dev     # 监听 :5173，代理 /api 到 :8088
```

### 默认配置

| 项 | 值 |
| :--- | :--- |
| 监听端口 | `8088`（`config.yaml` 可改） |
| 数据库 | SQLite（默认 `data/gateway.db`，可迁移到 MySQL） |
| 默认管理员 | 建站向导中自定义（示例默认 `admin / admin123`） |

### 数据与日志目录（单文件分发）

- **IP 库已嵌入二进制**：`ip2region.xdb`（11MB）编译进 `gatewayhub.exe`，运行时若 `data/ip2region.xdb` 缺失会自动解包；已存在（如用户自定义更新）则原样保留。
- **数据库**：默认 `data/gateway.db`，目录不存在时自动创建。
- **访问日志（与 NVS 同构）**：默认 `log/`，按 `类别 / 天 / 小时` 组织，每行一条 JSON（JSONL）：
  ```
  log/requests/2026-11-22/15.log      # 访问日志
  log/database/2026-11-22/15.log      # 预留：数据库日志（同一条归档链）
  log/archive/daily|weekly|monthly|yearly/
  ```
  字段与 NVS 的 `log/requests/<date>/<HH>.log` 完全对齐：`timestamp`（`2006-01-02 15:04:05`）、`user_id`（网关恒 0）、`method`、`path`、`query`、`ip`、`ua`、`status`、`size`、`latency`、`headers`、`body`；网关另加 `route_prefix`（命中的服务）与 `kind`（`path` / `subdomain` / `isolation`）。

  隔离网关（`/openlist/` 这类）与普通反代**共用同一个写入器**，未匹配 404、服务禁用、熔断降级也会记一条，日志不再有缺口。

- **日志分层归档（对齐 NVS 的 logarchive）**：日包 7z → 周包 tar.gz → 月包 tar.gz → 年包 tar.xz，启动即跑一次、之后按 `access_log.archive.interval`（默认 24h）循环；幂等（产物比源新则跳过），缺 7z 时日包自动降级为纯 Go tar.gz。也可手动触发：

  ```bash
  gatewayhub.exe -archive       # 只归档一轮后退出，适合放进系统计划任务
  ```

- 相关开关：`access_log.enabled` / `access_log.dir` / `access_log.body` / `access_log.redact_sensitive` / `access_log.archive.*`。
- 地图 GeoJSON（中国/世界）已随前端产物内嵌进二进制。

---

## 📦 发布（GitHub Actions）

多平台产物由 CI 自动构建，无需本地交叉编译：

1. 改版本号：`internal/handlers/settings.go` 的 `version` 与本文件顶部的版本一致；
2. 推到 `main`；
3. 打开 **Actions → Release → Run workflow**，版本号留空即读取代码里的值，勾选「自动创建并推送 tag」。

工作流会依次：校验版本一致性 → 创建并推送 `v{版本}` tag → 构建前端 → 并行交叉编译 10 个平台 → 发布 Release 并附 `SHA256SUMS`。

也可以直接推 tag：`git tag v1.5.7 && git push origin v1.5.7`（把版本号换成当前版本）。

PR 与 `main` 推送会跑 **CI**：`npm ci` + `vite build` + `go vet` + `go test` + 单平台编译（验证 `go:embed` 所需的前端产物齐全）。

> 前端产物 `web/dist` 不入库但被 `//go:embed all:web/dist` 引用，所以任何 `go build` 之前都必须先构建前端 —— CI 里由 `frontend` 作业产出后分发给 10 个编译任务复用。

本地全平台构建仍可用 `.\build-all.ps1`（Windows PowerShell）。

---

## 📁 目录结构

```
gatewayhub/
├── main.go                 # 入口：加载配置、初始化组件、启动服务（内嵌前端+IP库）
├── config.yaml             # 配置文件
├── build-all.ps1           # 一键自动化编译脚本（前端+全平台）
├── internal/
│   ├── config/             # 配置加载
│   ├── database/           # 数据库初始化 + 示例路由（无演示日志）
│   ├── models/             # 数据模型
│   ├── auth/               # bcrypt + JWT
│   ├── proxy/              # 反向代理引擎（热加载，DB 路由）
│   ├── isolation/          # 路径隔离网关（本轮新增，按职责分层）
│   │   ├── config/         #   服务配置表：解析/校验/默认值/热加载
│   │   ├── matcher/        #   首段匹配（不猜测归属）
│   │   ├── forward/        #   转发路径与转发头（纯函数）
│   │   ├── cookie/         #   Cookie 帽子（脱帽/加帽/Path 收紧）
│   │   ├── respond/        #   Location 与 HTML-CSS 字面量改写
│   │   ├── metrics/        #   按服务指标 + 重定向循环检测
│   │   ├── breaker/        #   连续失败熔断
│   │   ├── diag/           #   分类诊断日志 + 变更审计
│   │   └── engine/         #   单循环执行器（拦截-修正-转发-修正）
│   ├── health/             # 健康检查（绿/橙/红）
│   ├── stats/              # 异步日志写入（数据库统计）
│   ├── accesslog/          # 访问日志落盘（含请求头，按天/小时）
│   ├── geo/                # ip2region 地理位置解析（内嵌+自动解包）
│   ├── security/           # 限流 / WAF / 黑白名单
│   ├── migrate/            # 数据库迁移（SQLite↔MySQL）
│   ├── backup/             # 备份（手动+定时）
│   ├── handlers/           # API 处理器
│   └── router/             # 路由 + 中间件（安全/向导守卫）
├── web/                    # Vue 3 前端
│   ├── src/i18n/           # 中英文语言包
│   ├── src/views/          # 页面（看板/控制台/向导/安全/迁移/备份/健康...）
│   └── src/assets/         # 中国/世界地图 GeoJSON
├── data/                   # 运行数据（数据库 + IP 库）
└── logs/                   # 访问日志（按天/小时）
```

---

## 🔌 API 概览

| 分组 | 接口 |
| :--- | :--- |
| 建站向导 | `GET/POST /api/setup/status` `POST /api/setup/configure` |
| 路径隔离网关 | `GET /api/isolation/services` `GET /api/isolation/metrics` `GET /api/isolation/audit` `PATCH /api/isolation/services/:name/mode` `POST /api/isolation/reload` |
| 认证 | `POST /api/auth/login` `POST /api/auth/refresh` `PUT /api/auth/password` |
| 路由 | `GET/POST /api/routes` `PUT/DELETE /api/routes/:prefix` `PATCH .../status` |
| 友链 | `GET /api/friend-links`（公开） `POST /api/friend-links` `PUT/DELETE /api/friend-links/:id`（管理员） |
| 统计 | `/api/stats/overview` `/api/stats/routes` `/api/stats/trend` `/api/stats/geo` `/api/stats/cleanup` |
| 健康 | `GET /api/health` `POST /api/health/check` |
| 安全 | `/api/security/ips` `/api/security/apis` |
| 迁移 | `/api/migrate/info` `/api/migrate/test` `/api/migrate/run` |
| 备份 | `POST /api/backup` `GET /api/backup/list` `GET /api/backup/download` |
| 合规 | `GET /api/compliance` |
| 设置 | `GET/PUT /api/settings` |

认证方式：`Authorization: Bearer <jwt>`；响应统一 `{code, message, data}`。

---

## ⚙️ 关键配置（config.yaml）

```yaml
server:
  host: "0.0.0.0"
  port: 8088
  base_domain: "localhost" # 子域名后缀（空则禁用子域名路由）
  max_hops: 5             # 自指/嵌套链式转发的跳数上限，超过返回 508（<=0 用默认 5）

database:
  driver: sqlite
  dsn: data/gateway.db    # 数据库放 data/ 目录

geo:
  enabled: true
  db_path: data/ip2region.xdb  # 缺失时从二进制自动解包

access_log:                # 访问日志落盘（与 NVS 同构：类别/天/小时，JSONL）
  enabled: true
  dir: log                # 根目录 → log/requests/<date>/<HH>.log
  body: true              # 记录请求体片段（≤64KB；multipart 只记占位符）
  redact_sensitive: false # true 时落盘前脱敏 Cookie/Authorization/token 类字段（生产建议开）
  archive:                # 分层归档：日 7z → 周/月 tar.gz → 年 tar.xz
    enabled: true
    dir: ""               # 空则 {dir}/archive
    interval: 24h

security:                 # 安全防护
  enabled: true
  rate_limit: 100         # 每 IP 每秒请求数
  burst: 200              # 突发峰值
  ban_threshold: 5        # 连续超限 N 次封禁
  ban_duration: 300       # 封禁时长（秒）
  waf_enabled: true       # SQL/XSS 拦截
  waf_body_max: 524288    # WAF 请求体扫描上限（字节），超限跳过扫描

backup:
  enabled: true
  dir: "backups"
  interval: "24h"         # 定时备份间隔
  retain: 30              # 保留最近 N 份

setup:
  allow_remote: false     # 建站向导是否允许远程访问（默认仅 localhost）

isolation:                # 路径隔离网关（服务表在 isolation.yaml）
  enabled: true
  file: isolation.yaml
  watch: true             # 文件变更热加载
  watch_interval: 5
  audit_file: logs/isolation-audit.log
  trust_proxies: false    # 前面有可信反代才设 true（否则忽略客户端自带的转发头）

health_check:
  interval: 30
  slow_threshold: 1000    # 超过该毫秒判定为「降级」(橙)

stats:
  retain_days: 180        # 日志留存 6 个月（网络安全法第 23 条）
```

---

## 📄 合规与版权

- 中国地图/世界地图边界数据符合国家测绘标准；地图功能仅做流量可视化，不采集、不公开展示单个访问者精确坐标。
- 本项目采用 **GNU AGPL-3.0** 协议开源（详见 `LICENSE` 文件），`data/ip2region.xdb` 版权归 [ip2region](https://gitee.com/lionsoul/ip2region) 项目所有。
