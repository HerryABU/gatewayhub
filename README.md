# GatewayHub 多应用聚合网关

轻量级、带管理界面、支持动态配置的统一接入网关。所有后端服务通过**一个端口**对外暴露，路径格式 `/{转发名}/...`，路由规则可实时增删改、无需重启。

> 开源协议：GNU AGPL-3.0 · 后端 Go 1.21+ · 前端 Vue 3 + Vite + Element Plus + ECharts

---

## ✨ 功能特性

### 核心转发
- 基于路径前缀的反向代理（`net/http/httputil.ReverseProxy`）
- **子域名路由**：`java-order.localhost:8088/user` 与 `/{prefix}/user` 双模式并存，子域名命中时**零路径重写、零 `<base>` 注入**，纯透明转发（速度=源站）
- **多级路径前缀**（最长前缀优先）：`v2/beta` 可覆盖 `v2`，段边界对齐
- **WebSocket** 透明转发（HMR / 推送 / 聊天长连接不超时）
- 后端地址支持 `:8080`、`:8080/api/v1`、完整 URL（**含自定义端口**，如 `http://example.com:8443`）及 **`${ENV}` 环境变量**（如 `http://${ORDER_HOST}:8080`，未设置时创建路由报错）
- **外部网站代理**：完整 URL 目标天然支持（含 HTTPS），健康检查同样按 scheme/host 探测
- **响应内容「绝对改相对」重写**：HTML 属性/内联样式根绝对路径 → 文档相对路径（`./x`），并注入 `<base href="/{prefix}/">` 兜底解析；外链 CSS 的 `url()/@import` 按 CSS 文件自身位置换算相对路径；JS 中 API 调用相对化、资源引用与 `Location` 头保留前缀绝对（按模块/脚本基准解析最稳）
- 路由**毫秒级热加载**，无需重启
- 透传请求头，自动添加 `X-Forwarded-For` / `X-Real-IP` / `X-Forwarded-Proto`
- 超时/错误处理：502 / 504 / 404 / 503

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
- **访问日志（含完整请求头）**：默认 `logs/`，按 天/小时 组织，每行一条 JSON：
  ```
  logs/2026-08-25/17.log
  ```
  含 `time / method / path / status / latency_ms / client_ip / user_agent / route_prefix / headers`（完整请求头）。可用 `access_log.enabled` / `access_log.dir` 关闭或改目录。
- 地图 GeoJSON（中国/世界）已随前端产物内嵌进二进制。

---

## 📁 目录结构

```
gatewayhub/
├── main.go                 # 入口：加载配置、初始化组件、启动服务
├── config.yaml             # 配置文件
├── internal/
│   ├── config/             # 配置加载
│   ├── database/           # 数据库初始化 + 演示数据
│   ├── models/             # 数据模型
│   ├── auth/               # bcrypt + JWT
│   ├── proxy/              # 反向代理引擎（热加载）
│   ├── health/             # 健康检查（绿/橙/红）
│   ├── stats/              # 异步日志写入
│   ├── geo/                # ip2region 地理位置解析
│   ├── security/           # 限流 / WAF / 黑白名单
│   ├── migrate/            # 数据库迁移（SQLite↔MySQL）
│   ├── backup/             # 备份（手动+定时）
│   ├── handlers/           # API 处理器
│   └── router/             # 路由 + 中间件（安全/向导守卫）
├── web/                    # Vue 3 前端
│   ├── src/i18n/           # 中英文语言包
│   ├── src/views/          # 页面（看板/控制台/向导/安全/迁移/备份/健康...）
│   └── src/assets/         # 中国/世界地图 GeoJSON
└── data/ip2region.xdb      # 离线 IP 库
```

---

## 🔌 API 概览

| 分组 | 接口 |
| :--- | :--- |
| 建站向导 | `GET/POST /api/setup/status` `POST /api/setup/configure` |
| 认证 | `POST /api/auth/login` `POST /api/auth/refresh` `PUT /api/auth/password` |
| 路由 | `GET/POST /api/routes` `PUT/DELETE /api/routes/:prefix` `PATCH .../status` |
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
