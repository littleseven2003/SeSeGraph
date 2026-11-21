# SeSeGraph（SeSe图册）

面向个人单管理员的轻量级图集发布与画廊管理系统。本地打包上传图片，自动托管到 Telegram 私有频道，由内置流式网关对外提供图片访问，同时自动排版生成 telegra.ph 分享帖子，并在本地维护带封面缩略图的可视化图库。

项目为个人开源作品，遵循 AGPL-3.0 协议发布。

## 当前状态

`v0.3.0` 打通了从浏览器选图到 Telegram 私有频道存图、再到 Telegraph 发布与公开图片访问的完整链路：

1. 在发帖工作台拖入 ZIP、文件夹或多张图片，浏览器端完成解压、过滤与自然排序，可拖拽重排、剔除单张、点选封面。
2. 包内存在 `info.txt` / `meta.txt` / `readme.txt` 时按「首行标题」或「键值匹配」预填标题、说明与标签，缺失文件时静默留空。
3. 点击发布后先提交元数据与 400px WebP 封面创建任务，再逐张上传图片；中途关闭网页或断网后重新选择同一批文件即可按原序号补传缺失图片。
4. 后台队列按可配间隔逐张推送至私有频道，拿到远端文件标识后立即删除临时文件，页面显示「存图完成」；刷新页面仍可查看进度并重试失败图片。
5. 存图完成后在进度面板点「发布到 Telegraph」，后台逐页创建 `telegra.ph` 页面并补齐互链，完成后列出分享链接。
6. 发布时同时开启该帖的公开标记，访客通过 `GET /i/{short_code}` 流式取图；未发布的帖子（含升级前已存图的任务）保持私有。
7. 在详情抽屉里可在线编辑标题、说明与作者，也可一键撤回单帖；撤回后短码在源站立即 404，重新开启沿用原有公开授权，已缓存的副本可能仍显示一段时间。

画廊检索、设置页、防社死模糊与流量看护属于后续里程碑，未完成项在下方表格中以「开发中」标注，不与实际能力混淆。

## 功能特性

| 模块 | 说明 | 状态 |
| :--- | :--- | :--- |
| 单管理员鉴权 | 首次启动初始化管理员账号，密码以 bcrypt 哈希存储，HttpOnly Cookie 鉴权 | 已完成 |
| 发帖工作台 | ZIP / 文件夹 / 多图拖入，浏览器端解压、自然排序、拖拽排序、点选封面，可中断续传 | 已完成 |
| 浏览器端缩略图 | Canvas 压缩为宽 400px 的 WebP 封面图，服务器零解码压力 | 已完成 |
| 元信息自动读取 | 包内 `info.txt` 等文件按「首行标题」或「键值匹配」预填标题、说明与标签，缺省不报错 | 已完成 |
| 异步上传队列 | 持久队列逐张推送 Telegram 私有频道，共享发送间隔与 429 `retry_after` 退避，支持重启恢复、进度查询与失败重试 | 已完成 |
| Telegraph 发布 | 管理员确认后逐页创建 `telegra.ph` 帖子，超过 56 KiB 自动分篇并互链，失败重试复用已建页面 | 已完成 |
| 画廊与检索 | 封面卡片、关键词搜索、多标签组合筛选 | 开发中 |
| 防社死模糊 | 全局模糊开关，状态存于 `localStorage`，悬停临时去模糊 | 开发中 |
| 图片网关 | `GET /i/{short_code}` 流式反代，仅转发显式公开的帖子，短码随机高熵，隔离凭据与远端文件标识 | 已完成 |
| 流量看护 | 自然月出站流量统计、预警、超限熔断 | 开发中 |
| 单帖熔断 | 单帖撤回后短码立即 404 且不再请求 Telegram，重新开启秒级恢复；已缓存的副本可能仍显示 | 已完成 |
| 代理配置 | 直连 / 跟随系统环境变量 / 自定义 HTTP、SOCKS5，配置热生效 | 开发中 |
| 自动备份 | 定时 `VACUUM INTO` 热备，打包后经 Bot 送至私有频道 | 开发中 |
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

1. **直接部署（原生单二进制）**：`Makefile` 一键构建内嵌前端的可执行文件，支持 `linux/amd64`、`linux/arm64`、`darwin/arm64`、`windows/amd64` 交叉编译；提供 `deploy/sesegraph.service`、`deploy/nginx.conf.example`、`deploy/Caddyfile.example`。教程开发中。
2. **单容器 Docker**：多阶段 `Dockerfile`，默认监听 `8080`，数据目录 `/app/data`，内置 `HEALTHCHECK`。教程开发中。
3. **Docker Compose**：`docker-compose.yml` 编排应用与 Caddy，由 Caddy 依据 `.env` 中的 `DOMAIN` 自动签发 HTTPS；另提供不带 Caddy 的独立编排文件。教程开发中。

## Telegram 与 Telegraph 配置

图床依赖一个 Bot 与一个私有频道：

1. 通过 [@BotFather](https://t.me/BotFather) 创建 Bot，取得 Bot Token。
2. 新建私有频道，将该 Bot 添加为频道管理员并授予发布权限。
3. 将频道 ID 与 Token 填入进程环境变量（见 `.env.example`）。本版只在启动时读取进程环境变量，设置页与连通性自检随对应功能交付。
4. 确认 Bot 能访问 Telegram API 且对频道有发布权限。

相关环境变量：`TELEGRAM_BOT_TOKEN`、`TELEGRAM_CHANNEL_ID`、`UPLOAD_INTERVAL_MS`（发送间隔，默认 1500 毫秒）、`TELEGRAM_API_BASE`（可选，接入自建反代或本地联调）。

Telegraph 发布需要一个访问令牌，二选一：

- 通过环境变量提供 `TELEGRAPH_ACCESS_TOKEN`（可选 `TELEGRAPH_AUTHOR_NAME`、`TELEGRAPH_API_BASE`）；
- 登录后调用 `POST /api/settings/telegraph/create-account`，由服务端申请账户并落库，令牌不回显；重复或并发调用复用已有账户。

发布要求 `PUBLIC_URL` 是结构完整的 HTTPS 地址，不能包含查询串、片段或内嵌凭据，图片链接由它拼接。图片网关的并发上限由 `PROXY_CONCURRENCY` 控制，默认 25，满额立即返回 503。

Bot Token、Telegraph 令牌、频道信息与图片原始下载地址不会出现在任何接口响应、日志或页面中。

## 开发

```bash
# 完整构建：安装前端依赖、构建前端并把产物嵌入后端，生成单一可执行文件
make build VERSION=v0.3.0

# 运行后端测试
make test

# 前端单元测试与类型检查（工作台解包、排序、续传等纯逻辑）
cd frontend && pnpm test && pnpm typecheck

# 四个目标平台交叉编译，产物在 bin/ 下
make cross VERSION=v0.3.0
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
