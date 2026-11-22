# SeSeGraph（SeSe图册）

面向个人单管理员的轻量级图集发布与画廊管理系统。本地打包上传图片，自动托管到 Telegram 私有频道，由内置流式网关对外提供图片访问，同时自动排版生成 telegra.ph 分享帖子，并在本地维护带封面缩略图的可视化图库。

项目为个人开源作品，遵循 AGPL-3.0 协议发布。

## 当前状态

`v0.4.0` 打通了从浏览器选图到 Telegram 私有频道存图、再到 Telegraph 发布、画廊浏览、在线编辑与单帖撤回的完整链路：

1. 在发帖工作台拖入 ZIP、文件夹或多张图片，浏览器端完成解压、过滤与自然排序，可拖拽重排、剔除单张、点选封面。
2. 包内存在 `info.txt` / `meta.txt` / `readme.txt` 时按「首行标题」或「键值匹配」预填标题、说明与标签，缺失文件时静默留空。
3. 点击发布后先提交元数据与 400px WebP 封面创建任务，再逐张上传图片；中途关闭网页或断网后重新选择同一批文件即可按原序号补传缺失图片。
4. 后台队列按可配间隔逐张推送至私有频道，拿到远端文件标识后立即删除临时文件，页面显示「存图完成」；刷新页面仍可查看进度并重试失败图片。
5. 存图完成后在进度面板点「发布到 Telegraph」，后台逐页创建 `telegra.ph` 页面并补齐互链，完成后列出分享链接。
6. 发布时同时开启该帖的公开标记，访客通过 `GET /i/{short_code}` 流式取图；未发布的帖子（含升级前已存图的任务）保持私有。
7. 在详情抽屉里可在线编辑标题、说明与作者，也可一键撤回单帖；撤回后短码在源站立即 404，重新开启沿用原有公开授权，已缓存的副本可能仍显示一段时间。
8. 首页画廊以封面卡片浏览全部任务，支持关键词与多标签组合筛选、分页，详情抽屉查看图文清单并复制分享链接；列表按创建时间倒序，卡片展示首次发布时间。

设置页界面、流量看护、代理配置与自动备份属于后续里程碑，未完成项在下方表格中以「开发中」标注，不与实际能力混淆。

管理接口统一要求登录会话。撤回接口 `PATCH /api/posts/{id}/toggle` 的请求体为 `{"disabled": true}` 或 `{"disabled": false}`，表示要写入的目标状态而不是翻转：重复提交同一目标状态结果不变，因此网络超时后重试不会把已撤回的图片重新公开。该请求体上限 256 字节，缺字段返回 400。

## 功能特性

| 模块 | 说明 | 状态 |
| :--- | :--- | :--- |
| 单管理员鉴权 | 首次启动初始化管理员账号，密码以 bcrypt 哈希存储，HttpOnly Cookie 鉴权 | 已完成 |
| 发帖工作台 | ZIP / 文件夹 / 多图拖入，浏览器端解压、自然排序、拖拽排序、点选封面，可中断续传 | 已完成 |
| 浏览器端缩略图 | Canvas 压缩为宽 400px 的 WebP 封面图，服务器零解码压力 | 已完成 |
| 元信息自动读取 | 包内 `info.txt` 等文件按「首行标题」或「键值匹配」预填标题、说明与标签，缺省不报错 | 已完成 |
| 异步上传队列 | 持久队列逐张推送 Telegram 私有频道，共享发送间隔与 429 `retry_after` 退避，支持重启恢复、进度查询与失败重试 | 已完成 |
| Telegraph 发布 | 管理员确认后逐页创建 `telegra.ph` 帖子，超过 56 KiB 自动分篇并互链，失败重试复用已建页面 | 已完成 |
| 设置与自检 | 设置页维护令牌、频道与作者名，环境变量托管项只读，保存后图床热切换；并行连通性自检返回状态与耗时，令牌不回显 | 已完成 |
| 画廊与检索 | 封面卡片、关键词搜索、多标签组合筛选、分页、详情抽屉与分享复制，列表按创建时间倒序、展示首次发布时间 | 已完成 |
| 防社死模糊 | 全局模糊开关，状态存于 `localStorage`，悬停或键盘聚焦临时去模糊，触屏点按临时去模糊 | 已完成 |
| 图片网关 | `GET /i/{short_code}` 流式反代，仅转发显式公开的帖子，短码随机高熵，隔离凭据与远端文件标识 | 已完成 |
| 流量看护 | 按自然月统计图片出站流量，达到阈值经 Bot 提醒，超过上限后网关返回 503；后台可查看当月用量 | 已完成 |
| 单帖熔断 | 单帖撤回后短码立即 404 且不再请求 Telegram，重新开启秒级恢复；已缓存的副本可能仍显示 | 已完成 |
| 代理配置 | 直连 / 跟随系统环境变量 / 自定义 HTTP、HTTPS、SOCKS5，可分别对 Telegram 与 Telegraph 启用，保存后重建传输层；自定义地址含账密但不回显 | 已完成 |
| 自动备份 | 每日定时 `VACUUM INTO` 热备并打包 zip（可选含封面），经 Bot 送至频道；支持手动触发与本地下载 | 已完成 |
| 存储抽象 | `StorageProvider` 接口与 Telegram 提供方（`sendPhoto` / `sendDocument`） | 已完成 |

## 架构

- **后端**：Go 1.22+ 与 Gin，编译为单一静态二进制文件（`CGO_ENABLED=0`）。
- **数据库**：SQLite，纯 Go 驱动 `modernc.org/sqlite`，开启 WAL 与 `busy_timeout=5000`。
- **前端**：Vue 3 + Vite + TypeScript + Tailwind CSS，构建产物经 `go:embed` 嵌入二进制。
- **浏览器端解包**：`fflate` 解压与 Canvas 缩略图，服务器不承担解码与解压。
- **图片出口**：`io.Copy` 流式直通，不整包进内存；并发信号量限流。

目标运行环境为 1 核 / 512MB–1GB 内存的低配 VPS，空闲常驻内存控制在 30MB 以内。

## 部署方式

三种形态共用同一套命令行参数与环境变量配置（`--addr` / `LISTEN_ADDR`、`--data` / `DATA_DIR`、`--public-url` / `PUBLIC_URL`）。

1. **直接部署（原生单二进制）**：`make build` 生成 `bin/sesegraph`，`make cross` 交叉编译 `linux/amd64`、`linux/arm64`、`darwin/arm64`、`windows/amd64`。把二进制放到 `/opt/sesegraph`，按 `deploy/sesegraph.service` 装成 systemd 服务：数据目录由 `StateDirectory=sesegraph` 自动创建为 `/var/lib/sesegraph`，无需手工建目录或授权；再用 `deploy/nginx.conf.example` 或 `deploy/Caddyfile.example` 接入已有反向代理。
2. **单容器 Docker**：先建好数据目录并交给容器内的 uid 1000，再启动：
   ```bash
   mkdir -p data && sudo chown -R 1000:1000 data
   docker build -t sesegraph:local .
   docker run -d --name sesegraph -p 8080:8080 -v "$PWD/data:/app/data" sesegraph:local
   ```
   镜像基于 Alpine，内置 `HEALTHCHECK`（`/api/health`），默认监听 `8080`、数据目录 `/app/data`，容器内以 uid 1000 运行。
3. **Docker Compose**：`cp .env.example .env` 填好 `DOMAIN` 与 `ADMIN_PASSWORD`，然后 `docker compose up -d`。`docker-compose.yml` 编排应用与 Caddy：`init` 服务先建好 `./data` 并授权给 uid 1000，应用只在内部网络暴露 `8080`，Caddy 映射 `80/443` 并按 `DOMAIN` 自动签发与续期证书。只想用 Compose 启动应用、接入已有反向代理时改用
   `docker compose -f docker-compose.standalone.yml up -d`（只监听 `127.0.0.1:8080`）。Compose 里的 Caddy 未开启访问日志，避免 `X-Setup-Token` 这类自定义请求头被写入日志。

## Telegram 与 Telegraph 配置

图床依赖一个 Bot 与一个私有频道：

1. 通过 [@BotFather](https://t.me/BotFather) 创建 Bot，取得 Bot Token。
2. 新建私有频道，将该 Bot 添加为频道管理员并授予发布权限。
3. 将频道 ID 与 Token 填入进程环境变量（见 `.env.example`），也可以登录后在 `/settings` 页面保存，保存后图床立即切换。
4. 确认 Bot 能访问 Telegram API 且对频道有发布权限。

相关环境变量：`TELEGRAM_BOT_TOKEN`、`TELEGRAM_CHANNEL_ID`、`UPLOAD_INTERVAL_MS`（发送间隔，默认 1500 毫秒）、`TELEGRAM_API_BASE`（可选，接入自建反代或本地联调）。

## 流量看护与自动备份

`/settings` 的「流量看护」显示当月出站流量、请求次数与阈值状态；「自动备份」可立即生成一份备份并下载最近一份。

相关环境变量：

- `TRAFFIC_ALERT_GB`：月度预警阈值（GB），首次越过后通过 Bot 发一条提醒，同月只提醒一次；提醒没送达时不算已提醒，由后台协程在重试间隔到达后补发，不依赖新的图片请求；`0` 表示不提醒。
- `TRAFFIC_LIMIT_GB`：月度硬上限（GB），累计出站达到后图片网关直接返回 `503`，不再向上游取流；`0` 表示不熔断。计费按 `io.Copy` 实际写出的字节逐块累加，因此并发传输期间也能及时熔断，正在传输的连接照常完成。
- `BACKUP_ENABLED`：是否启用每日定时备份，默认关闭。
- `BACKUP_CRON`：定时表达式，只支持每天固定时刻（默认 `0 3 * * *`）。
- `BACKUP_CHANNEL_ID`：备份发送目标频道；留空时使用图床频道。
- `BACKUP_INCLUDE_THUMBS`：是否把封面缩略图一起打包，默认包含，设为 `false` 只备份数据库。

备份保存在数据目录的 `backups/` 下，默认保留最近 7 份；`VACUUM INTO` 生成的一致性快照只在打包期间存在，结束即删除。zip 先写临时文件，完整关闭后才改名为最终备份，下载接口不会读到半份文件；发送成功与否都执行保留策略。

备份包含数据库里的 Bot 与 Telegraph 凭据，因此发送前会读取目标会话属性：只有私聊或有私密频道/群（无公开用户名和公开别名）才会推送；目标为公开频道或无法确认时只保留本地备份，接口以 `warning` 说明原因。校验与发送固定使用同一份图床配置，校验期间保存设置切换频道也不会把备份发到新目标。

网络受限的环境可在 `/settings` 的「网络代理」中配置：

- `direct`：直连，不使用代理；
- `system`：跟随进程环境变量 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`（容器里挂载这些变量即可）；
- `custom`：自定义 `http://`、`https://` 或 `socks5://` 地址，可带账密。

Telegram 与 Telegraph 可以分别启用代理。保存后立即重建传输层，上传队列、图片网关、发布队列与连通性自检都使用新配置；自定义地址只报告是否已设置，响应与日志不出现地址取值。自定义模式下地址不能清空，需要清除时先切回直连。

这四项也可以由环境变量提供：`PROXY_MODE`、`PROXY_URL`、`PROXY_FOR_TELEGRAM`、`PROXY_FOR_TELEGRAPH`（`true` / `false`）。环境变量优先于设置页，托管后在界面只读。

Telegraph 发布需要一个访问令牌，二选一：

- 通过环境变量提供 `TELEGRAPH_ACCESS_TOKEN`（可选 `TELEGRAPH_AUTHOR_NAME`、`TELEGRAPH_API_BASE`）；
- 登录后调用 `POST /api/settings/telegraph/create-account`，由服务端申请账户并落库，令牌不回显；重复或并发调用复用已有账户。

发布要求 `PUBLIC_URL` 是结构完整的 HTTPS 地址，不能包含查询串、片段或内嵌凭据，图片链接由它拼接。图片网关的并发上限由 `PROXY_CONCURRENCY` 控制，默认 25，满额立即返回 503。

Bot Token、Telegraph 令牌、频道信息与图片原始下载地址不会出现在任何接口响应、日志或页面中。

## 开发

```bash
# 完整构建：安装前端依赖、构建前端并把产物嵌入后端，生成单一可执行文件
make build VERSION=v0.4.0

# 运行后端测试
make test

# 前端单元测试与类型检查（工作台解包、排序、续传等纯逻辑）
cd frontend && pnpm test && pnpm typecheck

# 四个目标平台交叉编译，产物在 bin/ 下
make cross VERSION=v0.4.0
```

首次启动前按 `.env.example` 准备配置；若希望用网页完成首次初始化，需先设置 `ADMIN_SETUP_TOKEN`，也可以直接用 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 通过环境变量建号。

只改前端时可以并行跑开发服务器，`/api` 会代理到本机 `8080`：

```bash
cd backend && go run ./cmd/server --addr 127.0.0.1:8080 --data ./data
cd frontend && pnpm dev
```

环境要求：Go 1.22+、Node.js 22.12+（Vite 8 要求）、pnpm 10+、Git。前端构建产物经 `go:embed` 嵌入二进制，直接部署只需交付单个可执行文件。

## 目录结构

```text
backend/     Go 服务（API、鉴权、配置、数据库、帖子仓储、存储提供方、上传队列、图片网关、Telegraph 发布器、前端内嵌）
frontend/    Vue 3 前端（初始化与登录、图集首页、发帖工作台及工具函数）
deploy/      systemd 服务模板与反向代理配置示例
Makefile     构建、交叉编译与测试入口
Dockerfile   单容器镜像
docker-compose.yml / Caddyfile   Compose 一键部署
```

## 版本与变更

版本遵循 SemVer 2.0.0，变更记录见 [CHANGELOG.md](CHANGELOG.md)。

## 许可

本项目以 GNU Affero General Public License v3.0 发布，全文见 [LICENSE](LICENSE)。
