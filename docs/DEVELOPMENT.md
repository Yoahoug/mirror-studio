# DEVELOPMENT.md — 本地开发指南

0.0.1 的后端是单个 Go 二进制：内嵌迁移、幂等种子、SSR 模板、SPA 静态资源与
进程内后台 worker（生图 + 小说可视化），数据落在 SQLite。没有 Postgres、
没有 Celery、没有 collectstatic，`docker/` 下也没有启动脚本——启动就是启动，
失败会打印原因并退出。

## 环境要求

- Go ≥ 1.26（`go.mod` 的 `go 1.26.0`；SQLite 驱动是纯 Go 的 `modernc.org/sqlite`，
  不需要任何 C 依赖）
- Node ≥ 20（仅前端构建需要）
- Python 3 仅用于 `tools/diff/`（差分验证，见下文）

## 常用命令

```bash
# 后端（默认监听 0.0.0.0:8000，Debug 默认关闭）
DEBUG=1 SECRET_KEY=dev-secret-key-0123456789abcdef \
FIELD_CRYPT_KEY=dev-field-crypt-key \
go run .

# 必填环境变量：SECRET_KEY（≥32 字符）、FIELD_CRYPT_KEY（≥8 字符）。
# PUBLIC_ORIGIN 在 Debug 下缺省为 http://127.0.0.1:8000，生产必须显式配置。

# 前端（两条 Vite 开发服务器，Debug 下 Go 会把页面指向它们）
npm install
npm run dev:workspace   # http://127.0.0.1:3000（代理 /api → 8000）
npm run dev:console     # http://127.0.0.1:3001
npm run watch:css       # SSR 页面样式（static/css/main.css）

# 构建与测试
npm run build           # build:css → build:workspace → build:console → build:ssr
go build -o bin/mirror-studio ./
go vet ./... && go test ./...

# 前端类型检查
npm run lint
```

`build:ssr` 是说明性空操作（SSR 页面由 Go 内嵌的 `internal/web/templates` 渲染，
只依赖 `build:css` 产出的 `static/css/main.css`），保留它是为了让 `npm run build`
的语义与 1.x 一致。

## 数据目录

默认相对工作目录：`db.sqlite3`（`DATABASE_URL`）、`media/`（`MEDIA_ROOT`）、
`novel/`（`NOVELVIZ_BOOK_DIR`）、`novelviz/uploads/`（`NOVELVIZ_UPLOAD_DIR`）。
首次启动会自动建库、跑迁移、幂等种子 siteconfig/默认渠道，并用
`ADMIN_USERNAME`/`ADMIN_PASSWORD` 创建或重置管理员。删除这些文件即重置环境。

## 差分验证（tools/diff，dev 资产）

`tools/diff/` 是重写期建立的验证工具，保留在仓库里作为回归门禁：
`python3 tools/diff/run.py --target python|go --out .reftmp/run-…` 回放
main/email/upstream 三档语料，`tools/diff/compare.py` 逐 profile 对比
trace+snapshot，`tools/diff/compare_upstream.py` 对比上游调用录制。
两侧必须在同一天录制；截图对比容差见 `docs/HANDOFF-0.0.1.md` §5。
需要参考实现（`.venv-ref/`，Django 5.2）在场；日常开发不依赖它。

## 配置

环境变量冻结集与语义见 `docs/api-contract-0.0.1.md` §11，逐项示例见 `.env.example`。
0.0.1 刻意移除了 `AUTO_*` 启动开关、`GENERATION_TASKS_RUN_INLINE`、
`MINIO_*`、`ALLOW_INSECURE_PROD` 等运行模式开关：行为固定为
迁移 → 种子 → 监听，`MOCK_PROVIDER_ENABLED` 与 `DEBUG` 同时为真才可用。

## 发布

见 `docs/RELEASING.md`：`VERSION` 只能由 `scripts/release.py` 修改，
镜像内的版本号来自构建时 `-ldflags -X main.version=$(cat VERSION)`。
