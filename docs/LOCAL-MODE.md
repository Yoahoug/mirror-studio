# LOCAL-MODE.md — 本地开发者模式（单端口 · 无数据库 · 无登录）

面向「打开就能改前端、就能出图」的场景：一条命令、一个端口启动工作台与管理台，
不需要数据库、不需要管理员账号、不需要登录注册，生成的图片直接落在仓库里。

```bash
npm install          # 首次一次
npm run local        # 单端口启动：本地 API + 工作台 + 管理台
```

启动后只有一个地址需要记住：

| 入口 | 地址 | 说明 |
| --- | --- | --- |
| 工作台 | http://127.0.0.1:8000/ | 文生图 / 图生图 / 小说可视化 / 历史记录 |
| 管理台 | http://127.0.0.1:8000/admin | 概览、用户、任务、渠道、系统设置 |
| 接口 | http://127.0.0.1:8000/api/… | 工作台接口；管理台接口在 `/console/api/…` |
| 状态页 | http://127.0.0.1:8000/__local | 数据目录与渠道状态 |

前端是 Vite 开发服务器（改代码即时热更新），它以 middleware 方式挂在同一个 HTTP 服务上，
因此热更新的 WebSocket 也走这个端口，对外只需要暴露一个地址。

## 端口与路径映射

| 路径 | 由谁处理 |
| --- | --- |
| `/`、`/workspace` | 工作台 SPA（`frontend/workspace`） |
| `/admin`、`/console` | 管理台 SPA（`frontend/console`） |
| `/api/…` | 本地 API（工作台接口） |
| `/console/api/…` | 本地 API（管理台接口） |
| `/downloads/…` | 本地 API（图片预览与下载） |
| `/healthz`、`/__local` | 本地 API（健康检查、状态页） |
| 其它无扩展名路径 | 回退到工作台外壳（等价于 SPA 回退） |

管理台同时接受 `/admin` 与 `/console`：本地模式推荐 `/admin`，
线上 Go 服务的页面路径是 `/workspace` 与 `/console`，两边互不冲突。

## 它与正式部署的区别

| | 本地开发者模式 | 正式部署（Go 单容器） |
| --- | --- | --- |
| 端口 | 单端口 8000（`/` 工作台、`/admin` 管理台、`/api` 接口） | 单端口 8000（`/workspace`、`/console`） |
| 数据 | `local-data/store.json` 一个 JSON 文件 | SQLite（`/app/data/db.sqlite3`） |
| 账号 | 固定 `local` 账号，自动登录，无登录/注册页 | 邮箱注册、验证码、登录风控 |
| 图片 | `local-data/media/`，写入即落盘 | 同左，`MEDIA_ROOT` |
| 出图 | 未配置渠道时本地合成占位图 | `PROVIDER_*` 真实渠道 |
| 对话/小说分析 | 未配置渠道时本地模拟 | `LLM_*` / `NOVELVIZ_*` 真实渠道 |
| 后端进程 | Node 零依赖脚本（`scripts/localapi/`） | Go 二进制，内嵌迁移与 worker |

本地模式只实现了工作台与管理台需要的那部分接口（约 35 个），
URL 形状、字段名与线上契约一致，所以两个前端应用不需要任何条件分支。

## 数据放在哪里

```text
local-data/store.json          任务、用户、渠道、站点配置、媒体索引
local-data/media/              生成图、参考图、小说场景图
local-data/novel/              上传的 EPUB
local-data/uploads/<会话>/     分片上传暂存（完成后自动清理）
```

`local-data/` 已被 `.gitignore` 忽略；想重置环境，删掉整个目录再启动即可。

## 接真实渠道（可选）

默认零配置，用本地占位图和本地模拟回复就能跑通全流程。
想接真实渠道时：

```bash
cp .env.local.example .env.local
# 填 PROVIDER_BASE_URL / PROVIDER_MODEL_NAME / PROVIDER_API_KEY（生图）
# 填 LLM_BASE_URL / LLM_MODEL_NAME / LLM_API_KEY（对话与小说场景分析）
npm run local
```

渠道变量名与线上 Go 服务一致，可以直接从 `.env` 复制过来。
没配 LLM 时：工作台对话会返回以「【本地模拟回复】」开头的本地建议，
小说场景分析会按正文段落顺序抽取场景，并在事件日志里写明「本地抽取」。

## 常用命令

```bash
npm run local          # 单端口启动（本地 API + 两个前端页面）
npm run local:api      # 只启动本地 API（8000），供下面两条命令代理
npm run local:check    # 接口自检：分片上传、图生图、EPUB、SSE、管理台

# 想按应用分开起开发服务器（各自端口、各自热更新），配合 npm run local:api 使用：
npm run dev:workspace  # http://127.0.0.1:3000（代理 /api、/console/api、/downloads → 8000）
npm run dev:console    # http://127.0.0.1:3001
# 本地 API 换了端口时，用 LOCAL_API_TARGET=http://127.0.0.1:<port> 指过去即可。
```

只想看 Go 后端 + 数据库的那套开发流程，见 `docs/DEVELOPMENT.md`。

## 已知边界

- **没有登录/登出**：账号由本地服务固定提供，工作台右上角的「登出」不会真的退出；
  管理台用 `local` 账号，权限始终是管理员。
- **不做 CSRF 校验**：本地服务只监听 `127.0.0.1`，且不存在跨账号身份，因此刻意省掉了
  CSRF 与风控；`/api/auth/csrf` 仍然返回 token，纯粹为了兼容前端代码路径。
- **邮件相关功能不可用**：注册、找回密码等入口在本地模式不会用到。
- **小说场景图由本地合成**：除非配置了 `NOVELVIZ_IMAGE_API_KEY`/`PROVIDER_API_KEY`，
  场景图是占位图；场景内容仍是按真实正文段落抽取的。
- **端口固定**：8000 被占用时会直接报错退出，不会悄悄换端口。
  被占用时（例如 Go 服务或 Docker 容器正在跑）在 `.env.local` 里改
  `LOCAL_API_PORT` 即可。
