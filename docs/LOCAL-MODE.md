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
| Comfy 生图 | 有：本地工作台直连本机 ComfyUI（`/api/comfy/*`） | 无：不暴露入口，也没有这些接口 |
| 后端进程 | Node 零依赖脚本（`scripts/localapi/`） | Go 二进制，内嵌迁移与 worker |

本地模式只实现了工作台与管理台需要的那部分接口（约 35 个），
URL 形状、字段名与线上契约一致，所以两个前端应用不需要任何条件分支。

**两者不是同一个进程里的开关**，而是两个独立的启动入口：
本地模式 = `npm run local`（Node 脚本，只在有 Node 的开发机上跑）；
线上 = Go 二进制（`go run .` 或容器里的同一个二进制，容器内没有 node）。
Go 的 `DEBUG=1` 不是“切到本地模式”，它只放宽 Cookie / SSL / 源站默认值
（并在未配置 `PUBLIC_ORIGIN` 时回落到 `http://127.0.0.1:8000`），
页面来源、端口与数据存储都不随它切换。

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

未配置生图渠道时出的是本地合成的**占位图**（渐变底 + 对角光带，不是真实画面）。
这种图在历史记录卡片上有「占位图」角标和说明，在创作板结果区也有提示，
不会和真实生图结果混在一起。

## 工作台悬浮窗对话（仅本地模式）

工作台右下角的悬浮气泡是本地专属的对话助手（线上没有这个组件）：
用来写/改生图提示词，写好一键「填回描述」填进当前创作板的描述框。

- **在界面里配置中转站**：悬浮窗 → 设置 → 填 Base URL（预置「线上中转站」与
  「本机 LM Studio」两档）、API Key、模型（可「获取模型列表」）、思考等级
  （off / low / medium / high）、temperature、自定义指令。配置存在
  `local-data/store.json`，密钥只回掩码不回原文；`.env.local` 里的 `LLM_*`
  是同名的回退配置，界面配置优先。
- **请求契约**：`POST {base_url}/v1/chat/completions`，SSE 流式；思考等级走
  `reasoning_effort`；**不发送 `max_tokens`**，思考过程不限长（沿线上要求的契约）。
- 界面同时显示正文与「思考过程」，可折叠；没有配置渠道时退回本地模拟回复，
  并在设置面板里提示去哪配。

## Comfy 生图板块（仅本地模式）

本地工作台侧边栏里还有一个「Comfy 生图」板块：直接调用本机的 ComfyUI（默认
`http://127.0.0.1:8188`）出图，工作流与参数契约来自 Comfy 工作台的既有资产。
**它只在本地开发者模式出现**：板块开关来自工作台入口文件上的 `data-mode="local"`，
线上 Go 的 SSR 模板不带这个标记，`/api/comfy/*` 也只由本地 API 实现，
所以线上既没有入口也不会调用本机进程。

首次使用：

```bash
# 在 .env.local 里配置启动命令（可以指向启动脚本的完整路径；留空则手动启动）
COMFY_START_CMD=D:\Users\25657\Desktop\Comfy\start-comfyui.bat
npm run local                       # 启动后打开工作台 → 侧边栏「Comfy 生图」
```

进入板块后点右上角「Comfy 设置」→「启动 ComfyUI」：命令在你本机后台执行，
日志写 `local-data/comfy-start.log`；冷启动要加载模型，通常 1-2 分钟才就绪，
界面会持续探测 `GET /system_stats` 并显示内存/显存读数。

板块要点：

- **四个模型**：6 步蒸馏（默认，约 15 秒/张，固定调度不可改步数）、官方
  Qwen-Image 2.1（CFG 1，25/35 步）、官方 Turbo 8 步（约 20 秒/张，固定 8 步
  日程、CFG 关闭）、无审查 NoctQ V4（CFG 3，25/35 步）。
  对应的工作流模板随仓库放在 `scripts/localapi/comfy/workflows/`。
- **图生图**：沿用文生图工作流就地改造（参考图接进编码器，采样 latent 换成参考图
  编码结果），出图比例跟随参考图、与所选比例无关；当前板块支持 1 张参考图
  （ComfyUI 契约上限 16 张）。
- **任务管线与线上一致**：状态、进度、取消、下载、历史记录全部复用同一套任务模型；
  Comfy 任务会标记 `engine=comfy`，在历史里点开会回到本板块恢复上下文，
  取消任务会给 ComfyUI 发 `/queue delete` 与 `/interrupt`。
- **失败信息**：ComfyUI 的原始报错（`exception_message`）会原样显示在任务里，
  不会只报「任务失败」。
- **内存占用**：ComfyUI 会把模型常驻内存（编码器 + 模型 + VAE，约 15G 内存 /
  数 G 显存）。工具栏和 Comfy 设置里都有「释放内存」按钮
  （`POST /api/comfy/free`，卸载模型并回收内存，下次出图重新加载）；
  每次生成成功后还会弹一次「保持常驻 / 释放内存」的选择，「保持常驻」在本次
  会话内不再追问（sessionStorage），关闭浏览器后恢复询问。
- **相关变量**：`COMFY_BASE_URL`、`COMFY_START_CMD`、`COMFY_TIMEOUT_MS`（默认 15 分钟）、
  `COMFY_FREE_MEMORY`（默认 0；显存紧张时设 1，会在每张前卸载模型）。

已知边界：

- 板块只负责「调用 + 启动」，**不会自动拉起** ComfyUI：启动按钮是唯一入口，
  也可以自己先运行 `start-comfyui.bat` 再回来点「刷新状态」。
- 没有「停止 ComfyUI」按钮：任务取消走 `/interrupt`；要停进程用桌面
  Comfy 文件夹里的 `stop-all.bat`，或按文档里的端口排查命令结束进程
  （「释放内存」只卸载模型，不停进程）。
- 本机显存/页面文件要求沿用 Comfy 契约：连续出图不稳时优先调大页面文件，
  再考虑打开 `COMFY_FREE_MEMORY=1`。

## TTS 语音合成板块（仅本地模式）

本地工作台侧边栏里的「TTS 语音合成」板块：调用 **E:\AI\TTS 的 CLI 契约**
（`TTS-调用契约.md`，机器可读版 `contract.json`）驱动本机四套 TTS 引擎合成语音。
**只在本地开发者模式出现**：与 Comfy 板块共用 `data-mode="local"` 开关，
`/api/tts/*` 也只由本地 API 实现，线上没有入口也没有这些接口。

首次使用：

```bash
# 前置：E:\AI\TTS 环境已就绪（四套引擎 + tts_cli.py）。位置不同时在 .env.local 里改：
TTS_PYTHON=E:\AI\TTS\Qwen3-TTS\.venv\Scripts\python.exe
TTS_CLI=E:\AI\TTS\tts_cli.py
npm run local                       # 启动后打开工作台 → 侧边栏「TTS 语音合成」
```

板块要点：

- **四种合成模式**：音色设计（文字描述造音色，可一步存成可复用音色）、
  音色复用（用音色库里的音色朗读）、声音克隆（参考音频 + 四引擎可选）、
  预设音色（Qwen 官方 9 音色 / GPT-SoVITS 社区音色）。
- **音色库**：直接读 `E:\AI\TTS\voices\<名>\meta.json`，可试听来源音频、
  一键切到「音色复用」；导入/导出走 CLI（`tts voices import/export`）。
- **引擎实例管理**：右上角「TTS 设置」里看实例状态、GPU 显存、启动/停止、各引擎日志。
  8G 显存同一时刻只能驻留一个引擎，跨引擎提交时前端会先弹确认
  （等价 CLI 的 `--switch`：先停当前引擎再加载目标引擎，8~70 秒）。
- **GPU 互斥**：TTS 合成与 ComfyUI 生图共用进程内的显卡锁（`/api/gpu/status` 查占用者），
  一边在跑时另一边提交会直接失败并提示「等它完成或先释放内存」，不会 OOM。
- **常驻内存与释放**：实例默认空闲 600 秒自动退出释放显存（`TTS_IDLE_TIMEOUT_SECONDS`）；
  连续合成 3 次后工作台会弹窗询问是否切换为常驻模式（不自动退出，省掉每次加载），
  「TTS 设置」里也有常驻开关；顶栏与设置里的「释放内存」按钮随时停实例释放显存
  （TTS = 停实例；Comfy 生图侧对应 `POST /api/comfy/release` 卸载模型；有任务在跑时会拒绝）。
- **高级参数**：按引擎展示常用子集（Qwen 温度/裁静音、GPT-SoVITS 语速/种子、
  IndexTTS 8 维情感向量/情感文本、MegaTTS3 p_w/t_w），经 `--set k=v` 直通，
  不认识的键由 CLI 按 `E_PARAM_UNKNOWN` 拒绝。
- **任务管线**：`/api/tts/tasks` 独立任务模型（queued → running → succeeded/failed/canceled），
  串行执行（契约并发=1），产物 WAV 落 `local-data/media` 走 `/downloads/`，
  历史记录里点开回到本板块；产物体检告警（近静音/偏短）会显示在任务里。
- **参考音频上传**：复用分片上传会话，类型 `tts_ref_audio`（≤30MB，
  wav/mp3/flac/m4a/ogg 等）；GPT-SoVITS 硬性 3~10 秒且参考文本必填。
- **相关变量**：`TTS_PYTHON`、`TTS_CLI`（指向非默认位置的 CLI）、
  `TTS_TIMEOUT_MS`（默认 30 分钟）、`TTS_IDLE_TIMEOUT_SECONDS`（默认 600，
  实例空闲自动退出释放显存）、`TTS_MAX_REF_AUDIO_MB`。

已知边界：

- **与 Comfy 板块共用一块显卡**：进程内 GPU 锁保证同一时刻只有一类任务在跑；
  但 ComfyUI 自己的 WebUI 或其他程序占显存时锁管不到，启动引擎前留意显存余量。
- **取消 = 杀 CLI 进程**：契约没有中断接口；已进入引擎的合成可能仍在后台完成，
  实例随后由空闲超时释放。
- 引擎首次加载 8~70 秒（冷启动更久）；合成固定开销约 15~40 秒，短句并不快。
- 本板块不做流式输出（契约固定整段落盘）；`--takes N` 多条产物并列展示。

## 常用命令

```bash
npm run local          # 单端口启动（本地 API + 两个前端页面）
npm run local:api      # 只启动本地 API（8000），供下面两条命令代理
npm run local:check    # 接口自检：分片上传、图生图、EPUB、SSE、管理台、Comfy、TTS
                       # 默认自己起一个隔离实例（临时数据目录 + 空闲端口），
                       # 自检产生的任务/图片跑完即删，不写 local-data；
                       # 要自检已运行实例：LOCAL_API_BASE=http://127.0.0.1:8000 npm run local:check

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
  场景图是占位图；场景内容仍是按真实正文段落抽取的。占位图在历史记录卡片上有
  「占位图」角标与说明，配置真实渠道后新出的图不会带这个标记。
- **端口固定**：8000 被占用时会直接报错退出，不会悄悄换端口。
  被占用时（例如 Go 服务或 Docker 容器正在跑）在 `.env.local` 里改
  `LOCAL_API_PORT` 即可。
