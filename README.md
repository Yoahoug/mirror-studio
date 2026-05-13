# 绘镜 Mirror Studio

绘镜 Mirror Studio 是一个面向朋友小圈子和自托管场景的 AI 生图工作台。你可以接入自己的图片模型和 LLM 渠道，开放给朋友注册使用，支持参考图上传、文生图、图生图，以及上传 EPUB 小说后自动分析剧情并生成场景图。

项目默认采用 Django 单体后端承载两个 React 工作台，配合 PostgreSQL、后台 worker 和本地文件存储即可运行。代码结构适合公开仓库初始化、私有部署和二次修改。

## 能做什么

- 文生图、图生图与历史记录管理。
- EPUB 小说上传、剧情场景分析、场景图生成。
- 邮箱注册、验证码、登录风控和用户隔离。
- 每个账号每日免费次数，后台可调整默认额度和单个用户额度。
- 管理台维护用户、任务、系统配置、图片渠道和 LLM 渠道。
- 大文件分片上传，适合通过反向代理或 Cloudflare Tunnel 部署。

## 技术栈

- 后端：Django + Gunicorn + PostgreSQL。
- 前端：React + Vite + TypeScript。
- 存储：默认本地文件目录；保留 MinIO 兼容能力。
- 部署：Docker Compose，包含 `postgres`、`web`、`worker` 三个服务。

## 快速部署

复制环境变量模板：

```bash
cp .env.example .env
```

编辑 `.env`，最少需要改这些：

- `SECRET_KEY`
- `PUBLIC_ORIGIN`
- `POSTGRES_PASSWORD`
- `DATABASE_URL` 中的数据库密码
- `ADMIN_USERNAME` / `ADMIN_PASSWORD` / `ADMIN_EMAIL`
- `PROVIDER_BASE_URL` / `PROVIDER_MODEL_NAME` / `PROVIDER_API_KEY`
- `LLM_BASE_URL` / `LLM_MODEL_NAME` / `LLM_API_KEY`
- 邮件 SMTP 配置

启动：

```bash
docker compose up -d --build
```

默认访问：

- 工作台：`http://服务器IP:8000/workspace`
- 管理台：`http://服务器IP:8000/console`
- 兼容入口：`http://服务器IP:8000/admin`

如果你把 `HTTP_PORT` 改成其他值，按实际端口访问。

## 最小环境变量说明

### 站点

`PUBLIC_ORIGIN` 是最关键的公开访问地址，例如：

```bash
PUBLIC_ORIGIN=https://image.example.com
```

程序会用它自动推导默认的 `ALLOWED_HOSTS` 和 `CSRF_TRUSTED_ORIGINS`。只有多域名部署时才需要手动补充：

```bash
ALLOWED_HOSTS=image.example.com,www.image.example.com
CSRF_TRUSTED_ORIGINS=https://image.example.com,https://www.image.example.com
```

### 生图渠道

公开版统一使用 `PROVIDER_*`：

```bash
PROVIDER_BASE_URL=https://your-image-provider.example.com
PROVIDER_API_PATH=/v1/images/generations
PROVIDER_MODEL_NAME=gpt-image-2
PROVIDER_API_KEY=your-api-key
```

旧的 `YUNMENG_*` 变量仍有兼容读取，但不再推荐新部署使用。

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

如果通过 Cloudflare Tunnel、Nginx、宝塔反代或其他 HTTPS 入口访问，必须保证外部域名和协议能传到 Django。

`.env` 建议：

```bash
PUBLIC_ORIGIN=https://image.example.com
USE_X_FORWARDED_HOST=1
SESSION_COOKIE_SECURE=1
CSRF_COOKIE_SECURE=1
SECURE_SSL_REDIRECT=1
```

反向代理需要保留这些头：

```nginx
proxy_set_header Host $host;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

项目里的 `docker/nginx/default.conf` 已经对 `/api/uploads/` 关闭请求缓冲，并保留 `X-Forwarded-Host` / `X-Forwarded-Proto`。如果你的 Cloudflare Tunnel 直接打到 `web:8000`，请在 tunnel 或上游代理中传递同等头部。

## 大文件上传

默认分片配置：

```bash
RESUMABLE_UPLOAD_CHUNK_SIZE=1048576
RESUMABLE_UPLOAD_SESSION_TTL_SECONDS=86400
NOVELVIZ_MAX_EPUB_UPLOAD_BYTES=83886080
```

如果前面还有 Nginx 或其他代理，请确认请求体限制大于 `NOVELVIZ_MAX_EPUB_UPLOAD_BYTES`。项目自带 Nginx 示例为：

```nginx
client_max_body_size 96m;
proxy_request_buffering off;
```

## 本地开发

macOS / Linux：

```bash
.venv/bin/python --version 2>/dev/null || python3 -m venv .venv
source .venv/bin/activate
.venv/bin/pip install -r requirements/dev.txt
npm install
npm install --prefix frontend/workspace
npm install --prefix frontend/console
cp .env.example .env
.venv/bin/python manage.py migrate
.venv/bin/python scripts/seed_siteconfig.py
.venv/bin/python scripts/seed_demo_data.py
.venv/bin/python manage.py runserver
```

前端开发服务：

```bash
npm run watch:css
npm run dev --prefix frontend/workspace
npm run dev --prefix frontend/console
```

Windows PowerShell 可参考 `scripts/start-local.ps1` 等脚本。

## 构建前端静态资源

```bash
npm run build:css
npm run build --prefix frontend/workspace
npm run build --prefix frontend/console
.venv/bin/python manage.py collectstatic --noinput
```

## 初始化数据

容器启动时由这些变量控制：

```bash
AUTO_APPLY_MIGRATIONS=1
AUTO_SEED_SITECONFIG=1
AUTO_INIT_APP_DATA=1
AUTO_COLLECTSTATIC=1
```

其中 `AUTO_INIT_APP_DATA=1` 会创建或更新管理员账号，并根据 `PROVIDER_*` / `LLM_*` 初始化默认模型渠道。

## 常用命令

```bash
# 启动
 docker compose up -d --build

# 查看日志
 docker compose logs -f web
 docker compose logs -f worker

# 进入 Django shell
 docker compose exec web python manage.py shell

# 重新初始化站点配置
 docker compose exec web python scripts/seed_siteconfig.py

# 重新初始化管理员和默认渠道
 docker compose exec web python scripts/seed_demo_data.py
```

## 目录结构

```text
apps/                 Django 应用
config/               Django 配置、URL、ASGI/WSGI
frontend/workspace/   用户工作台 React 应用
frontend/console/     管理台 React 应用
requirements/         Python 依赖
scripts/              初始化与辅助脚本
static_src/           Tailwind 源样式
static/               构建后静态入口资源
templates/            Django 模板
docker/               容器启动脚本和 Nginx 示例
```

## 许可证

本项目采用 [PolyForm Noncommercial License 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0/) 许可协议。

简单来说：不可商用，可二改。

你可以在非商业目的下学习、研究、自托管、复制、分发和二次修改本项目；分发原始版本或二次修改版本时，需要附带该许可证或许可证链接，并保留原项目来源说明。第三方依赖、模型服务和外部素材按其各自许可证或服务条款执行。
