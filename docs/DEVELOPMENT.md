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

## 两条本地开发路线

| | `npm run local`（本地开发者模式） | `go run .`（与生产同构） |
| --- | --- | --- |
| 需要 Go / SECRET_KEY / 管理员 | 都不需要 | 需要 |
| 端口 | 单端口 8000：`/` 工作台、`/admin` 管理台 | 单端口 8000：`/workspace` 工作台、`/console` 管理台 |
| 数据 | `local-data/store.json` + `local-data/media/` | SQLite + `media/` |
| 登录 | 无（固定本地账号自动登录） | 注册/登录/验证码全流程 |
| 适合 | 改前端、看 UI、演示、跑通业务链路 | 改后端、验证迁移/风控/上游调用 |

后者是本文档的默认流程，前者见 `docs/LOCAL-MODE.md`。两条路线的前端代码完全相同：
`npm run local` 里 Vite 以 middleware 方式挂在本地 API 的 HTTP 服务上（单端口 + HMR）；
`go run .` 时 Go 直接服务 `go:embed` 打进二进制的构建产物，改前端后需要
`npm run build:workspace` / `build:console` 重新构建并重启进程（页面里不再有指向
其它端口的开发分支）。需要 HMR 时按需单独起 `npm run dev:workspace` /
`npm run dev:console`（各自端口，通过 `LOCAL_API_TARGET`，默认
`http://127.0.0.1:8000`，代理 `/api`、`/console/api`、`/downloads`）——
它们只服务自己的页面，不改变后端的单端口形态。

## 常用命令

```bash
# 后端（默认监听 0.0.0.0:8000，Debug 默认关闭）
DEBUG=1 SECRET_KEY=dev-secret-key-0123456789abcdef \
FIELD_CRYPT_KEY=dev-field-crypt-key \
go run .

# 必填环境变量：SECRET_KEY（≥32 字符）、FIELD_CRYPT_KEY（≥8 字符）。
# PUBLIC_ORIGIN 在 Debug 下缺省为 http://127.0.0.1:8000，生产必须显式配置。

# 前端（产物经 go:embed 打进二进制：改完前端要重新构建并重启上面的进程）
npm install
npm run build:workspace # 工作台 → static/workspace/
npm run build:console   # 管理台 → static/console/
npm run watch:css       # SSR 页面样式（static/css/main.css）

# 需要 HMR 时（各自单起，页面由 Vite 自己服务，接口代理回 8000）
npm run dev:workspace   # http://127.0.0.1:3000（代理 /api → 8000）
npm run dev:console     # http://127.0.0.1:3001
# 或者直接用单端口的本地开发者模式：npm run local（见 docs/LOCAL-MODE.md）

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
`MINIO_*`、`ALLOW_INSECURE_PROD`、`WORKSPACE_VITE_DEV_URL` /
`CONSOLE_VITE_DEV_URL` 等运行模式开关：行为固定为
迁移 → 种子 → 监听，`MOCK_PROVIDER_ENABLED` 与 `DEBUG` 同时为真才可用；
页面始终由这一个进程的一个端口提供，不指向任何外部开发服务器。
（`docs/api-contract-0.0.1.md` 与 `docs/baseline/` 是 0.0.1 基线的历史记录，
包含当时仍未移除的开关，不再随代码更新。）

## 发布

见 `docs/RELEASING.md`：`VERSION` 只能由 `scripts/release.py` 修改，
镜像内的版本号来自构建时 `-ldflags -X main.version=$(cat VERSION)`。
