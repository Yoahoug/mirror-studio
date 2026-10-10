# 绘镜 Mirror Studio

绘镜 Mirror Studio 是一个面向朋友小圈子和自托管场景的 AI 生图工作台。你可以接入自己的图片模型和 LLM 渠道，开放给朋友注册使用，支持参考图上传、文生图、图生图，以及上传 EPUB 小说后自动分析剧情并生成场景图。

0.0.1 是一次整体重写：后端为**单个 Go 二进制**（内嵌 SQLite 迁移、幂等种子、SSR 模板、SPA 静态资源与进程内后台 worker），前端为**一个 npm 包**内的两个 React 工作台，部署为**单容器 + 单卷 + SQLite**。不兼容 1.x 的 Django/Postgres 数据结构，属于全新基线。

## 能做什么

- 文生图、图生图与历史记录管理。
- EPUB 小说上传、剧情场景分析、场景图生成。
- 邮箱注册、验证码、登录风控和用户隔离。
- 每个账号每日免费次数，后台可调整默认额度和单个用户额度。
- 管理台维护用户、任务、系统配置、图片渠道和 LLM 渠道。
- 大文件分片上传，适合通过反向代理或 Cloudflare Tunnel 部署。

## 技术栈

- 后端：Go 1.26 单二进制，SQLite（纯 Go 驱动，无 CGO），进程内 worker。
- 前端：React + Vite + TypeScript，构建产物内嵌进二进制。
- 存储：本地文件目录（`/app/data` 一个卷）。
- 部署：Docker Compose，单个 `app` 服务。

## 快速部署

复制环境变量模板：

```bash
cp .env.example .env
```

编辑 `.env`，最少需要改这些：

- `SECRET_KEY`（≥32 字符）
- `FIELD_CRYPT_KEY`（渠道 API Key 的字段加密密钥，缺失拒绝启动）
- `PUBLIC_ORIGIN`
- `ADMIN_USERNAME` / `ADMIN_PASSWORD` / `ADMIN_EMAIL`
- `PROVIDER_BASE_URL` / `PROVIDER_MODEL_NAME` / `PROVIDER_API_KEY`
- `LLM_BASE_URL` / `LLM_MODEL_NAME` / `LLM_API_KEY`
- 邮件 SMTP 配置

启动：

```bash
# 方式一：使用 GitHub Actions 构建并发布到 GHCR 的镜像（推荐，服务器无需构建）
docker compose pull && docker compose up -d

# 方式二：在服务器上就地构建
docker compose up -d --build
```

镜像由 `.github/workflows/container.yml` 在每次推送到 `main` 时构建并发布到
`ghcr.io/yoahoug/mirror-studio`（版本标签跟随 `VERSION`，当前 `0.0.1`；
公开包，拉取无需登录）。构建走 GitHub Actions 缓存，
重复构建会复用 npm/Go 依赖层。`IMAGE_TAG` 可覆盖 compose 使用的镜像标签。

启动过程固定为：建库迁移 → 幂等种子（站点配置、管理员、默认渠道）→ 监听；失败会打印原因并退出，用 `docker compose logs app` 查看。

容器只暴露一个端口（`HTTP_PORT`，默认 8000），工作台、管理台与接口都在它上面。

默认访问：

- 工作台：`http://服务器IP:8000/workspace`
- 管理台：`http://服务器IP:8000/console`

如果你把 `HTTP_PORT` 改成其他值，按实际端口访问。

## 数据与备份

所有可变数据在容器内的 `/app/data` 一个卷里：

```text
/app/data/db.sqlite3          SQLite 主库（含 -wal/-shm 伴生文件）
/app/data/media/              生成图与参考图
/app/data/novel/              小说可视化产物
/app/data/novelviz/uploads/   EPUB 分片上传暂存
```

备份 = 备份这个卷；升级 = 换镜像重新 `up -d`，迁移在启动时自动应用。

## 最小环境变量说明

完整清单见 `.env.example`；变量语义是差分冻结契约的一部分（`docs/api-contract-0.0.1.md` §11）。

### 站点

`PUBLIC_ORIGIN` 是最关键的公开访问地址，例如：

```bash
PUBLIC_ORIGIN=https://image.example.com
```

多域名部署时补充：

```bash
CSRF_TRUSTED_ORIGINS=https://www.image.example.com
```

### 生图渠道

公开版统一使用 `PROVIDER_*`：

```bash
PROVIDER_BASE_URL=https://your-image-provider.example.com
PROVIDER_API_PATH=/v1/images/generations
PROVIDER_MODEL_NAME=gpt-image-2
PROVIDER_API_KEY=your-api-key
```

### 小说分析 LLM

小说场景分析使用 `LLM_*`：

```bash
LLM_BASE_URL=https://your-llm-provider.example.com
LLM_API_PATH=/v1/chat/completions
LLM_MODEL_NAME=gpt-5.4
LLM_API_KEY=your-api-key
```

小说图片生成默认复用 `PROVIDER_*`，只需要按需调整尺寸、质量和并发：

```bash
NOVELVIZ_IMAGE_SIZE=1024x1024
NOVELVIZ_IMAGE_QUALITY=high
NOVELVIZ_IMAGE_CONCURRENCY=1
```

### 邮件

QQ 邮箱示例：

```bash
EMAIL_HOST=smtp.qq.com
EMAIL_PORT=465
EMAIL_USE_SSL=1
EMAIL_USE_TLS=0
EMAIL_HOST_USER=your_qq@qq.com
EMAIL_HOST_PASSWORD=your_qq_smtp_auth_code
DEFAULT_FROM_EMAIL=绘镜 Studio <your_qq@qq.com>
```

`EMAIL_HOST_PASSWORD` 填 SMTP 授权码，不是 QQ 登录密码。

## Cloudflare Tunnel / 反向代理部署

如果通过 Cloudflare Tunnel、Nginx、宝塔反代或其他 HTTPS 入口访问，`.env` 建议：

```bash
PUBLIC_ORIGIN=https://image.example.com
USE_X_FORWARDED_HOST=1
SECURE_SSL_REDIRECT=1
```

反向代理需要保留这些头：

```nginx
proxy_set_header Host $host;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

经 Cloudflare Tunnel 直达容器时，请在 tunnel 或上游代理中传递同等头部。`TRUST_X_FORWARDED_FOR=0`（默认）时应用取 TCP 对端地址做风控计数；仅当反代会覆盖 `X-Forwarded-For` 时才置 1，否则客户端可伪造来源 IP。

上传大文件（EPUB）时确认代理的请求体限制大于 `NOVELVIZ_MAX_EPUB_UPLOAD_BYTES`，并对上传接口关闭请求缓冲。

## 大文件上传

默认分片配置：

```bash
RESUMABLE_UPLOAD_CHUNK_SIZE=1048576
RESUMABLE_UPLOAD_SESSION_TTL_SECONDS=86400
NOVELVIZ_MAX_EPUB_UPLOAD_BYTES=83886080
```

## 本地开发

### 只想先跑起来看看（无需数据库 / 无需登录）

```bash
npm install
npm run local
```

一条命令、一个端口拉起全部页面与接口（默认 http://127.0.0.1:8000）：
工作台在 `/`，管理台在 `/admin`。没有数据库、没有管理员账号、没有登录注册，
固定使用本地 `local` 账号；生图不配渠道时由本地合成占位图，
图片直接落在 `local-data/media/`。想接真实生图 / 对话渠道，
把 `.env.local.example` 复制成 `.env.local` 填密钥即可。详见 `docs/LOCAL-MODE.md`。

### Go 后端（正式部署同构的开发流程）

macOS / Linux（需要 Go ≥ 1.26 与 Node ≥ 20）：

```bash
npm install
npm run build            # 前端产物写入 static/，由 go:embed 打包
DEBUG=1 SECRET_KEY=dev-secret-key-0123456789abcdef \
FIELD_CRYPT_KEY=dev-field-crypt-key \
go run .
```

页面与接口由这一个进程的一个端口提供（默认 8000）；前端资源内嵌在二进制里，
改完要重新 `npm run build:workspace` / `build:console` 再重启进程：

```bash
npm run build:workspace  # 工作台；管理台为 npm run build:console
npm run watch:css        # SSR 页面样式（static/css/main.css）
```

需要热更新时，单独起 Vite 开发服务器（页面由它自己服务，接口代理回 8000）：
`npm run dev:workspace`（:3000）或 `npm run dev:console`（:3001）；
也可以直接用单端口的 `npm run local`（见下方）。

测试与类型检查：

```bash
go test ./...
npm run lint
```

详见 `docs/DEVELOPMENT.md`（Go 本地开发）、`docs/LOCAL-MODE.md`（免数据库本地模式）、
`docs/FRONTEND.md`（前端结构与构建）、`docs/RELEASING.md`（版本与发布）。

## 常用命令

```bash
# 启动 / 重建
docker compose up -d --build

# 拉取 GitHub 发布的镜像
docker compose pull && docker compose up -d

# 查看日志
docker compose logs -f app

# 版本发布（VERSION 只能由它修改）
python3 scripts/release.py 0.0.2 --dry-run
python3 scripts/release.py 0.0.2
```

## 目录结构

```text
main.go                单二进制入口（内嵌 static/、迁移、tzdata）
internal/              Go 后端全部包（auth/web/generator/media/novel/console/...）
frontend/workspace/    用户工作台 React 应用
frontend/console/      管理台 React 应用
frontend/shared/       两应用共用的 Vite 配置
static_src/            SSR 页面 Tailwind 源样式
static/                构建产物（go:embed 打包，不提交）
scripts/release.py     发布脚本（VERSION 的唯一写入者）
scripts/localapi/      本地开发者模式的零依赖 Node 服务（npm run local）
.github/workflows/     CI：构建容器镜像并发布到 GHCR（container.yml）
tools/diff/            差分验证工具（开发资产）
docs/                  契约、审查、验证与开发文档
```

## 友情链接

- [LINUX DO](https://linux.do/)：一个开放活跃的社区。

## 许可证

本项目采用 [PolyForm Noncommercial License 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0/) 许可协议。

简单来说：不可商用，可二改。

你可以在非商业目的下学习、研究、自托管、复制、分发和二次修改本项目；分发原始版本或二次修改版本时，需要附带该许可证或许可证链接，并保留原项目来源说明。第三方依赖、模型服务和外部素材按其各自许可证或服务条款执行。
