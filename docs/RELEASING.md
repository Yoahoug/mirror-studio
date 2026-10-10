# RELEASING.md — 发布流程

版本信息只有两个来源：`VERSION`（镜像构建注入）与 `CHANGELOG.md`
（应用内「更新说明」的数据源）。除此之外没有任何硬编码版本号。

## 版本的单源规则

- **`VERSION` 只能由 `scripts/release.py` 修改**，禁止手工编辑。
- 应用启动时解析 `CHANGELOG.md` 的最新 `## [X.Y.Z] - 日期` 段生成
  「更新说明」（`internal/config/changelog.go`），找不到段时回落
  `APP_RELEASE_FALLBACK_VERSION`。所以**先写日志、再发版**：
  `CHANGELOG.md` 里必须已有目标版本的 `## [X.Y.Z]` 段（内容不能为空），
  否则 `release.py` 拒绝发布。
- 镜像构建用 `-ldflags "-X main.version=$(cat VERSION)"` 注入版本号。

## 发布步骤

```bash
# 0. 前置：工作区干净、在发布分支上
git status --porcelain   # 必须为空

# 1. 在 CHANGELOG.md 的 [Unreleased] 段写好本次变更（release.py 会把它
#    改写为 `## [X.Y.Z] - <今天>` 并作为 tag 消息与「更新说明」）

# 2. 试运行（只校验，不写文件、不打 tag）
python3 scripts/release.py 0.0.2 --dry-run

# 3. 正式发布：改写 CHANGELOG、写 VERSION、提交 chore(release)、
#    打 annotated tag v0.0.2 并推送（--no-tag / --no-push 可跳过对应步骤）
python3 scripts/release.py 0.0.2

# 4. 构建、验证、部署（镜像由 GitHub Actions 自动构建并发布到 GHCR）
git checkout v0.0.2
docker compose pull              # 或 docker compose up -d --build 就地构建
docker compose up -d
docker compose logs app          # 启动固定为：迁移 → 幂等种子 → 监听
curl -fsS http://127.0.0.1:8000/login -o /dev/null
```

`release.py` 只用 Python 标准库；发布动作是「一个 commit + 一个 annotated tag」，
tag 消息即该版本的更新说明。

## 容器镜像（GitHub Actions → GHCR）

`.github/workflows/container.yml` 在推送到 `main`、推送 `v*` 标签或手动触发时，
用仓库根 `Dockerfile` 构建镜像并发布到 `ghcr.io/yoahoug/mirror-studio`：

- **标签**：`VERSION` 的内容（当前 `0.0.1`）、`latest`（默认分支）、
  `sha-<短SHA>`；推送 `v*` 标签时额外产出不带 `v` 的版本标签。
- **缓存**：构建使用 GitHub Actions 缓存（`type=gha, mode=max`），
  前端 `npm ci`、Go 模块下载与各构建段都会命中，重复构建显著加快。
- **部署端**：`docker-compose.yml` 默认引用该镜像（`IMAGE_TAG` 可覆盖标签）；
  包是公开的，`docker compose pull` 无需登录（改成私有包则需 `docker login ghcr.io`）。

## 部署形态（0.0.1）

- **单容器**：镜像 = distroless/static + 一个 Go 静态二进制 + `CHANGELOG.md`。
  无 node、无 Python、无 shell、无包管理器；静态资源、SSR 模板、SQLite 迁移、
  时区库全部内嵌。
- **单卷**：`/app/data` 一个卷容纳 `db.sqlite3`（含 WAL 伴生文件）、`media/`、
  `novel/`、`novelviz/uploads/`。备份 = 备份这个卷；升级 = 换镜像（迁移在
  启动时自动应用，向前兼容由迁移链保证）。
- **数据库**：只支持 SQLite（`DATABASE_URL` 接受 `sqlite:///相对路径`、
  `sqlite:////绝对路径` 或裸路径）；传 Postgres DSN 会拒绝启动。
- **进程内 worker**：生图与小说可视化的后台任务在同一进程内，不需要也不存在
  独立 worker 服务；容器多副本部署会因 SQLite 单写者语义受限，0.0.1 按
  单实例设计（`restart: unless-stopped` 的一个 `app` 服务）。

## 必填环境变量

`SECRET_KEY`（≥32 字符）、`FIELD_CRYPT_KEY`（≥8 字符，字段加密密钥，
缺失即拒绝启动——这是 0.0.1 的安全底线，不再有明文回退）、生产环境还需
`PUBLIC_ORIGIN`。完整清单与默认值见 `.env.example` 与
`docs/api-contract-0.0.1.md` §11。

## 安全头与反代

生产默认 `SECURE_SSL_REDIRECT=1`：明文 HTTP 301 到 https，判断依据是
TLS 直连或 `X-Forwarded-Proto` 匹配 `SECURE_PROXY_SSL_HEADER_VALUE`
（默认 `https`）。经 Cloudflare Tunnel / Nginx 前置时保持
`USE_X_FORWARDED_HOST=1`，且仅当反代会覆盖 `X-Forwarded-For` 时才置
`TRUST_X_FORWARDED_FOR=1`（否则客户端可伪造来源 IP，影响风控计数）。
