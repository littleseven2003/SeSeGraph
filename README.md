# SeSeGraph（SeSe图册）

面向个人单管理员的轻量级图集发布与画廊管理系统。本地打包上传图片，自动托管到 Telegram 私有频道，由内置流式网关对外提供图片访问，同时自动排版生成 telegra.ph 分享帖子，并在本地维护带封面缩略图的可视化图库。

项目为个人开源作品，遵循 AGPL-3.0 协议发布。

## 当前状态

`v0.1.0` 基础框架已可运行：Go 服务内嵌前端构建产物，提供单管理员初始化与登录、受保护的管理首页、健康检查与版本页脚，SQLite 首次启动自动建库。发布工作台、图片网关与部署教程随后续里程碑补齐，未完成项以「开发中」标注，不与实际能力混淆。

## 功能特性

| 模块 | 说明 | 状态 |
| :--- | :--- | :--- |
| 单管理员鉴权 | 首次启动初始化管理员账号，密码以 bcrypt 哈希存储，HttpOnly Cookie 鉴权 | 开发中 |
| 发帖工作台 | ZIP / 文件夹拖入，浏览器端解压、自然排序、拖拽排序、点选封面 | 开发中 |
| 浏览器端缩略图 | Canvas 压缩为宽 400px 的 WebP 封面图，服务器零解码压力 | 开发中 |
| 元信息自动读取 | 包内 `info.txt` 等文件按可配规则预填标题与说明，缺省不报错 | 开发中 |
| 异步上传队列 | 后台逐张推送 Telegram 私有频道，内置发送间隔与 429 `retry_after` 精准退避 | 开发中 |
| Telegraph 发布 | 组装外链生成 `telegra.ph` 帖子，超过 56KB 自动分篇并互链 | 开发中 |
| 画廊与检索 | 封面卡片、关键词搜索、多标签组合筛选 | 开发中 |
| 防社死模糊 | 全局模糊开关，状态存于 `localStorage`，悬停临时去模糊 | 开发中 |
| 图片网关 | `GET /i/{short_code}` 流式反代，短码随机高熵，隔离凭据与远端文件标识 | 开发中 |
| 流量看护 | 自然月出站流量统计、预警、超限熔断 | 开发中 |
| 单帖熔断 | 单帖禁用后图片立即 404，重新开启秒级恢复 | 开发中 |
| 代理配置 | 直连 / 跟随系统环境变量 / 自定义 HTTP、SOCKS5，配置热生效 | 开发中 |
| 自动备份 | 定时 `VACUUM INTO` 热备，打包后经 Bot 送至私有频道 | 开发中 |
| 存储抽象 | `StorageProvider` 接口，v1.0 实现 Telegram 提供方 | 开发中 |

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

## Telegram 配置入口

图床依赖一个 Bot 与一个私有频道：

1. 通过 [@BotFather](https://t.me/BotFather) 创建 Bot，取得 Bot Token。
2. 新建私有频道，将该 Bot 添加为频道管理员并授予发布权限。
3. 将频道 ID 与 Token 填入环境变量（见 `.env.example`），或在设置页填写。
4. 使用设置页的连通性自检确认 Bot 可访问 Telegram API 且对频道有发布权限。

Bot Token、频道信息与图片原始下载地址不会出现在任何接口响应、日志或页面中。

## 开发

```bash
# 完整构建：安装前端依赖、构建前端并把产物嵌入后端，生成单一可执行文件
make build VERSION=v0.1.0

# 运行测试（仅后端，前端类型检查见 make typecheck）
make test

# 四个目标平台交叉编译，产物在 bin/ 下
make cross VERSION=v0.1.0
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
backend/     Go 服务（API、配置、数据库、存储、Telegraph、网关、队列、网络、备份）
frontend/    Vue 3 前端（上传工作台、画廊、任务、设置）
deploy/      systemd 服务模板与反向代理配置示例
Makefile     构建、交叉编译与测试入口
Dockerfile   单容器镜像
docker-compose.yml / Caddyfile   Compose 一键部署
```

## 版本与变更

版本遵循 SemVer 2.0.0，变更记录见 [CHANGELOG.md](CHANGELOG.md)。

## 许可

本项目以 GNU Affero General Public License v3.0 发布，全文见 [LICENSE](LICENSE)。
