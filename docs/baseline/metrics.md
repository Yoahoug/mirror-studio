# 0.0.1 基线指标（Django 1.x，重写前）

采集时间：2026-10-09。采集环境：macOS (darwin 27.0.0 arm64)、Docker 29.4.0 (OrbStack)、
Go 1.26.5、Python 3.12.13（参考环境 venv）、Node 20。

## 规模

| 指标 | 数值 |
| --- | --- |
| 后端 Python 代码（`apps/` + `config/` + `scripts/` + `manage.py`） | 11,845 行 |
| Python 文件数（全仓，排除 venv/node_modules） | 155 |
| Django 模板 | 19 个文件 / 1,397 行 |
| Django 迁移文件 | 38 |
| 前端 TypeScript/TSX | 11,432 行 / 38 个文件（workspace 21 + console 17） |
| 测试 | 6 个测试文件 / 1,690 行 / 60 个测试方法（generator 651 + mediahub 651 + core 153 + users 149 + console 89 + billing 1） |

## 部署形态

| 指标 | 数值 |
| --- | --- |
| 容器服务数 | **3**（`postgres`、`web`、`worker`） |
| 卷数 | **3**（`postgres_data`、`media_data`、`static_volume`） |
| 基础镜像 | `python:3.11-slim` + 独立 `node:20-slim` 构建阶段 |
| 运行时依赖（`requirements/prod.txt` 条目） | **14** |
| 启动命令 | `sh /app/docker/start-web.sh`（wait_for_db → migrate → seed → collectstatic → gunicorn） |

## 体积与内存

| 指标 | 数值 | 采集方式 |
| --- | --- | --- |
| 应用镜像体积 | **698 MB** | `docker build -t mirror-studio-baseline:py .`（`python:3.11-slim` 基底，含 gunicorn、Pillow、ebooklib、requests、cryptography、argon2-cffi、whitenoise、dj-database-url、minio、腾讯云 SDK 等） |
| web 容器空载内存 | **62.13 MiB** | `gunicorn --workers 1 --worker-class gthread --threads 4`，空闲 25s 后 `docker stats` |
| worker 容器空载内存 | **63.67 MiB** | `python manage.py run_generation_worker`，空闲 20s 后 `docker stats` |
| 应用进程空载内存合计 | **约 126 MiB** | 上述两者之和；未计入 `postgres` 容器 |

## 参考环境可运行性确认

- 参考环境：`.venv-ref`（Django 5.2.18 + argon2-cffi + cryptography + dj-database-url + Pillow +
  ebooklib + beautifulsoup4 + requests + minio + aiohttp + whitenoise + django-htmx）。
- 启动方式：`DJANGO_SETTINGS_MODULE=config.settings.dev`、`DATABASE_URL=sqlite:///<abs>`、
  `GENERATION_TASKS_RUN_INLINE=1`、`NOVELVIZ_RUN_INLINE=1`、`WORKSPACE_VITE_DEV_URL=" "`、
  `CONSOLE_VITE_DEV_URL=" "`（空白会被 `(env(...) or default).strip()` 归一为 `""`，从而回落到
  生产静态资源标签而不是 Vite 开发服务器）。
- 全流程可执行：注册 → 登录 → 文生图 / 图生图 → 历史与下载 → 分片上传 → EPUB 上传与场景图 →
  小说任务 SSE → 控制台全部操作，全部在差分语料（main 211 步 / email 22 步 / upstream 77 步）中跑通。

## 端到端生成任务耗时（参考环境，mock 渠道）

直接从基线 SQLite（`.reftmp/baseline/app.sqlite3`）的
`generator_generatetask.submitted_at/finished_at` 与
`novelviz_novelvisualizationtask.created_at/finished_at` 读取：

| 阶段 | 数值（6 次采样） |
| --- | --- |
| 单张文生图任务（提交 → `succeeded`，mock 渠道、`GENERATION_TASKS_RUN_INLINE=1`） | 25.2 / 33.4 / 21.7 / 25.3 / 25.2 / 20.7 ms，中位 **25 ms** |
| `submitted_at` → `started_at`（认领延迟） | 0.5–0.9 ms |
| 小说可视化任务（EPUB 解析 + 2 场景 LLM 规划 + 2 张场景图） | **20.5 ms** |

mock 渠道不含真实上游网络时间，因此该数值反映的是调度与状态机开销；Go 端只要在同一量级内
即可视为等价（真实部署中上游 RT 占绝对主导）。

## 截图基线

`docs/baseline/screenshots/`（1280×900，DPR 1，12 张）：`home`、`pricing`（404）、`login`、
`register`、`forgot-password`、`account`、`account-security`、`account-history`、`workspace`、
`console`、`public-error`（404 页面渲染）。

## 差分语料基线

`.reftmp/run-ref/`：

| profile | 步数 | 说明 |
| --- | --- | --- |
| `main` | 211 | 认证、账号设置、额度、生成（4 文生图 + 2 图生图）、媒体、上传、小说、控制台、SSR 已登录页 |
| `email` | 22 | 邮箱验证码路径（`EMAIL_VERIFICATION_ENABLED=1`） |
| `upstream` | 77 | 上游请求录制（`MOCK_PROVIDER_ENABLED=0`，指向本地假上游） |

每条 profile 产出 `trace.json`（归一化 HTTP 轨迹）与 `snapshot.json`（19 张应用表的 SQLite 快照）。
`upstream-final.jsonl` 记录 13 条真实上游请求（方法、路径、头白名单、请求体归一化摘要）。
