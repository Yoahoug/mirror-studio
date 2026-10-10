# 会话交接（0.0.1 重写，切换到新设备继续）

> **本文是历史记录，勿当作当前待办。** 它是 Django → Go 重写期（分支
> `refactor/0.0.1`，已并入 `main`、远端分支已删）为切设备写的交接单。
> 其中「下一步」与状态已过时：S6 容器/发布文档已补齐（见 `docs/RELEASING.md`），
> 安全审计清单的现状见 `docs/security-audit-0.0.1.md` 表格的「状态」列，
> `tools/diff/` 保留为留档资产（`.venv-ref/` 不在仓库里，重建后 §2 的命令才可用）。
> 当前开发入口是 `docs/DEVELOPMENT.md`，发布入口是 `docs/RELEASING.md`。

分支：`refactor/0.0.1`（已推送到 `origin`），基线文档在 `docs/`。
最后更新：2026-10-10。

---

## 1. 一句话现状

Django → Go 的重写**已完成 S0–S4 与 S5 的验证部分**：全部域已移植、`go test ./...`
全绿、差分语料三档回放（main 211 / email 22 / upstream 77 步）与上游请求录制
**逐字节一致**、11 张页面截图像素级一致。S5 的安全审计已完成并产出台账
（`docs/security-audit-0.0.1.md`），其中 3 项已修（含 1 个 HIGH），**其余 22 项待修**；
S6（容器/发布文档）与 S7（删除 Python、tag、最终报告）尚未开始。

**下一步的唯一入口**：`docs/security-audit-0.0.1.md` 末尾的「按严重度排序的修复清单」。

---

## 2. 常用命令（全部在仓库根执行）

```bash
# 编译与测试（提交前必须全绿）
go build -o bin/mirror-studio .
go test ./... -count=1
go vet ./...

# 差分语料：两侧必须在同一天录制，否则日期派生字段必然不同（见 §5）
python3 tools/diff/run.py --target python --out .reftmp/run-ref
python3 tools/diff/run.py --target go     --out .reftmp/run-go
for p in main email upstream; do
  python3 tools/diff/compare.py .reftmp/run-ref/$p .reftmp/run-go/$p; done

# 上游请求录制对比（独立于 compare.py）
python3 tools/diff/compare_upstream.py .reftmp/run-ref .reftmp/run-go

# 前端构建（产物路径与内容契约）
npm run build

# 截图对比（需要浏览器工具，见 §6）
python3 tools/diff/shots_run.py --target go --db <db> --port 8000 --out .reftmp/shots/go &
#   然后在 ZCode 的 node_repl 里跑 tools/diff/shots.mjs 的 captureAll()
.venv-ref/bin/python tools/diff/compare_shots.py .reftmp/shots/python .reftmp/shots/go
```

## 3. 环境依赖（新设备上必须先准备）

| 依赖 | 用途 | 说明 |
| --- | --- | --- |
| Go 1.26 | 构建 | `go.mod` 声明 `go 1.26.0` |
| Python 3.12 + `.venv-ref/` | **参考实现**（Django）与截图对比脚本 | `.venv-ref/bin/python`；缺失则无法做差分验证与像素对比（Pillow 在其中） |
| Node 20 + 根 `node_modules` | 前端构建与 Vite 开发服务器（截图需要） | `npm ci` |
| 端口 8011 / 8099 / 8000 / 3000 / 3001 | 后端差分 / 假上游 / 截图后端 / 两个 Vite | 8013、8012 曾出现残留进程，脚本会检查占用 |

`.venv-ref/` 与 `node_modules/` 都不在 git 里，新设备上需要重建：

```bash
python3 -m venv .venv-ref
.venv-ref/bin/pip install Django==5.2.18 argon2-cffi cryptography dj-database-url \
    Pillow ebooklib beautifulsoup4 requests minio aiohttp whitenoise django-htmx
npm ci
```

## 4. 本会话完成的提交（按时间）

| commit | 内容 |
| --- | --- |
| `537af75` | 统一图片管线（新增 `internal/imageutil`）并补齐 EXIF 方向；三处重复实现合并 |
| `e7a0437` | **修复** 44 字节 `FIELD_CRYPT_KEY` 二次派生（安全清单第 9 项的 HIGH）+ 去掉 `v1:` 前缀 + `internal/crypt` 首个测试（用参考实现的密文做交叉验证） |
| `5edcd98` | S4 截图对比工具（`compare_shots.py`/`shots_run.py`/`bridge_database.py`/`shots.mjs` 空白帧防护）+ 安全掩码修复 |
| 待提交 | `docs/security-audit-0.0.1.md`、本文件 |

在此之前：`685c225`（前端包合并与死代码删除）、`5a4ce22`（Go 后端与全域移植）、
`ae92e8c`（断言翻译台账）、`0db0bd2`（VERSION/CHANGELOG/发布脚本）。

## 5. 已知陷阱（踩过的坑，别再踩）

1. **差分录制对日期敏感**。`last_reset_date` 等字段直接取自当天日期：如果参考侧是
   昨天录的、Go 侧今天录，`main` 会出现 165 条差异。**必须同一天重录两侧**再比较。
2. **`shots_run.py` 曾把 Python 后端子进程留成孤儿**（`start_new_session` + 只捕获
   SIGINT），导致下一次「就绪」探测其实打在旧进程上、截图采到了错误的服务。现已修
   （SIGTERM 走清理、启动前查端口占用、healthz 后再验子进程存活）；**如果看到
   `ready` 但后端日志显示启动失败，先 `lsof -ti:PORT` 查残留**。
3. **浏览器会返回空白帧**：DOM 有文本、`readyState=complete`，但截图是纯白或超时。
   `shots.mjs` 现在会识别空白帧、重新导航后重试，三次失败**报错**而不是记录空白图。
   不要把空白 PNG 当成产品差异。
4. **0.0.1 拒绝运行在 Django 迁移过的库上**（`table "users_user" already exists`），
   这是**设计如此**（0.0.1 是全新基线）。截图对比需要两侧渲染同一批数据，故用
   `tools/diff/bridge_database.py` 把参考库的数据灌进 Go 自建库；它会先断言共有表
   列完全一致，不一致就中止。
5. **Fernet/签名等格式差异不会被差分语料覆盖**（`FIELD_CRYPT_KEY` 在语料里是短密钥，
   `scripts/export_deploy_bundle.py` 生成的 44 字节密钥路径从未被跑到）——审计才能发现。
   新增这类"只在某些取值下分叉"的逻辑时，要补单元测试而不是只依赖差分。
6. **`internal/crypt` 与 `internal/imageutil` 以前没有测试**，现在 `crypt` 有了；改这两处
   务必跑 `go test ./internal/crypt/`。
7. 截图对比的容差语义：`--pixel-tolerance 8`（通道差 ≤8 不计）、`--threshold 0.0001`。
   已接受差异必须用 `--accepted PAGE=理由` 显式传入，理由会打印在工具输出里。

## 6. 浏览器工具的用法

截图必须从 ZCode 的 `mcp__node_repl__js`（主会话作用域，子代理不可用）里驱动，
用官方 bootstrap 拿到 `agent.browsers`，再：

```js
const shots = await import(pathToFileURL("<repo>/tools/diff/shots.mjs").href);
await shots.captureAll({ browsers, baseUrl: "http://127.0.0.1:8000",
  outDir: "<out>", onLine: nodeRepl.write, onLog: nodeRepl.write });
```

SPA 页面在 DEBUG=1 下指向 Vite 开发服务器（3000/3001），**截图前要先起两台**
`npx vite --config frontend/{workspace,console}/vite.config.ts --port=3000|3001 --host=127.0.0.1`，
否则 workspace/console 采不到内容。

## 7. 剩余工作

### 7.1 S5 剩余：安全审计 22 项待修
见 `docs/security-audit-0.0.1.md` 附表。优先级：3 项 P0（SSR 登录绕过锁定、
`MOCK_PROVIDER_ENABLED` 生产校验、配额结算原子性）→ 9 项 P1 → 10 项 P2。
每修完一组要重跑差分（§2 命令）确认无回归。

### 7.2 S5 收尾：`docs/verification-0.0.1.md`
内容：安全清单 12 项逐项结论 + Go 实现位置、差异验证记录（语料回放/上游录制/
截图对比的数字）、例外与理由、剩余风险。审计文档已给出 12 项的原始结论，可直接改写。

### 7.3 S6 容器与发布
- `Dockerfile` 三段：node 构建两套 SPA（含根 `build:css`）→ `CGO_ENABLED=0` 静态二进制
  （`go:embed` 静态资源）→ distroless/static；镜像内不得有 node/Python/工具链/测试文件。
- `docker-compose.yml` 只保留 `app` 服务与一个卷 `/app/data`（`db.sqlite3`、`media/`、`novel/`）。
  **注意现有 compose 仍传 `DATABASE_URL=postgresql://…`，而 Go 只接受 sqlite，会直接拒绝启动。**
- `-ldflags` 注入版本；`VERSION` 由 `scripts/release.py` 唯一修改。
- 文档：`docs/RELEASING.md`、`docs/DEVELOPMENT.md`、`docs/FRONTEND.md`（第 11/12/13 节要求）。
- 验收：`docker compose up -d --build` 一次成功、镜像内无 node/Python、静态资源与媒体
  鉴权语义不变。

### 7.4 S7 收尾
- 确认差分通过（现在已通过）后删除 Python 后端与其脚本：`apps/`、`config/`、`manage.py`、
  `requirements/`、`scripts/{make_update_zip.py,export_deploy_bundle.py,seed_*.py,*.ps1}`、
  `docker/nginx/`、`start-beat.sh`、`start-worker.sh`、`wait_for_db.py`、`templates/`
  （SSR 模板已由 Go 内嵌复刻）等，按第 7 节「不移植清单」执行。
- README 重写、`VERSION`=0.0.1、打 tag `v0.0.1`、输出**最终报告（第 15 节格式，仅一次）**。
- 报告中需单列的既有条目（已收集）：`README.md`/`Dockerfile`/`scripts/*.ps1` 中指向旧前端
  目录结构的失效引用；`build:ssr` 是说明性空操作；保留但未使用的前端依赖
  （`@google/genai`、`express`、`dotenv`、`tsx`、`@types/express`）；`tools/diff` 作为
  dev 资产保留的理由；`internal/generator/store.go:372-374` 等无调用者的函数应删除。

## 8. 产出文档索引

| 文件 | 内容 |
| --- | --- |
| `docs/api-contract-0.0.1.md` | S0 冻结的逐字节接口契约 |
| `docs/baseline/metrics.md` | 重写前的规模/部署/性能基线与截图基线 |
| `docs/review-0.0.1.md` | S1 代码审查清单（67 条分级问题与移植决策） |
| `docs/go-architecture.md` | 分层与冻结的跨包 API 表 |
| `docs/test-translation-0.0.1.md` | Django 60 个测试断言 → Go 测试的翻译台账 |
| `docs/security-audit-0.0.1.md` | **本次安全审计与修复台账（下一步入口）** |
| `docs/HANDOFF-0.0.1.md` | 本文件 |
| `CHANGELOG.md` / `VERSION` / `.env.example` | 发布契约与配置契约 |
