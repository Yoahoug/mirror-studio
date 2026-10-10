# 更新日志

本文件是所有版本信息的单一来源：应用内的「更新说明」弹窗由服务端在启动时解析本文件生成，
`scripts/release.py` 也依赖本文件判断目标版本是否已归档。

## [Unreleased]

### Added

- 本地开发者模式的「Comfy 生图」板块（仅本地模式，线上不暴露）：工作台侧边栏新增入口，
  直连本机 ComfyUI（`/api/comfy/*` 只由本地 Node 服务实现，Go 服务没有这些接口），
  Comfy 设置界面可一键启动 ComfyUI 并查看状态与显存；
  提供 6 步蒸馏、官方 Qwen-Image 2.1、官方 Turbo 8 步、无审查 NoctQ V4 四套工作流
  （模板随仓库 `scripts/localapi/comfy/workflows/` 版本化），支持文生图/图生图、步数与种子；
  任务复用既有任务管线（状态、取消、下载、历史记录，历史里点开回到该板块）。
  配置项：`COMFY_BASE_URL` / `COMFY_START_CMD` / `COMFY_TIMEOUT_MS` / `COMFY_FREE_MEMORY`
  （详见 `docs/LOCAL-MODE.md`）
- 本地模式的内存管理：`POST /api/comfy/free`（卸载模型并回收内存）、Comfy 工具栏与
  设置里的「释放内存」按钮，以及每次生成成功后的「保持常驻 / 释放内存」询问
  （选常驻后本会话不再追问）
- 本地开发者模式的「TTS 语音合成」板块（仅本地模式，线上不暴露）：工作台侧边栏新增入口，
  通过 `E:\AI\TTS` 的 CLI 契约（`--json` 信封）调用本机四套 TTS 引擎（Qwen3-TTS 设计/克隆/
  预设、GPT-SoVITS、IndexTTS 2.5、MegaTTS3），支持音色设计（可一步存成可复用音色）、
  音色复用、参考音频克隆（分片上传 `tts_ref_audio`）、预设/社区音色四种模式与音色库
  （试听/使用）；高级参数按引擎白名单直通 `--set`；任务走独立 `/api/tts/tasks` 管线，
  产物 WAV 落媒体库、历史记录点开回到该板块。配置项：`TTS_PYTHON` / `TTS_CLI` /
  `TTS_TIMEOUT_MS` / `TTS_IDLE_TIMEOUT_SECONDS` / `TTS_MAX_REF_AUDIO_MB`
  （详见 `docs/LOCAL-MODE.md`）
- 本机 GPU 资源互斥：TTS 合成与 ComfyUI 生图共用进程内显卡锁（`GET /api/gpu/status`
  查占用者），同一时刻只允许一类任务跑；一边占用时另一边提交会失败并提示等待或释放。
  「释放内存」接口：TTS = `POST /api/tts/release`（停常驻实例），生图 = `POST /api/comfy/release`
  （卸载模型）；TTS 侧另有常驻内存开关（`--init-set idle_timeout=0`）与连续合成后的
  「让模型常驻内存？」询问弹窗
- 工作台悬浮窗对话（仅本地模式）：接入真实中转站，界面里可配置 Base URL / API Key /
  模型列表 / 思考等级（off~high）/ temperature / 自定义指令；SSE 流式显示正文与
  思考过程，提示词可一键填回创作描述；按线上契约走 `reasoning_effort` 且不发送
  `max_tokens`（思考不限长）；未配置渠道时退回本地模拟回复
- 本地占位图在界面上可识别：任务接口返回 `channel_name` 与 `placeholder` 标记，
  历史记录卡片带「占位图」角标和说明，创作板结果区同步提示，避免和真实生图混淆
- 本地开发者模式 `npm run local`：无需数据库、管理员、登录与注册，固定本地账号自动登录，
  只暴露工作台与管理台；未配置渠道时由本地合成占位图与本地模拟回复，
  生成结果落在 `local-data/`（详见 `docs/LOCAL-MODE.md`）
- 本地开发者模式只占用一个端口（默认 8000）：工作台在 `/`、管理台在 `/admin`，
  Vite 以 middleware 方式挂在同一个 HTTP 服务上（含 HMR），不再需要 3000/3001
- GitHub Actions 工作流 `.github/workflows/container.yml`：推送 `v*` 版本标签或
  手动触发时构建容器镜像并发布到 `ghcr.io/yoahoug/mirror-studio`
  （普通提交不触发构建；标签跟随 `VERSION`，当前 `0.0.1`）；
  构建使用 Actions 构建缓存（`type=gha, mode=max`），后续构建复用 npm/Go 依赖层

### Changed

- 工作台与管理台页脚的版本号改为从仓库根目录的 `VERSION` 注入
  （构建与本地开发同一套逻辑），本地显示与线上一致，不再是写死的 `v1.2`
- `npm run local:check` 默认自己起一个隔离实例（临时数据目录 + 空闲端口），
  自检产生的任务、图片与上传跑完即删，不再往 `local-data/` 的工作台历史里
  灌占位图；要自检已运行实例用 `LOCAL_API_BASE=http://127.0.0.1:8000 npm run local:check`
- 服务端不再把页面指向 Vite 开发服务器：Go 始终加载 `go:embed` 打包的产物，
  `/workspace`、`/console` 与接口同处一个端口，容器只需暴露一个端口；
  改前端后用 `npm run build:workspace` / `build:console` 重建并重启，
  需要 HMR 时用单端口的 `npm run local` 或按需单独起 Vite 开发服务器
- `docker-compose.yml` 默认使用 GHCR 上发布的镜像（`IMAGE_TAG` 可覆盖），
  同时保留 `docker compose up -d --build` 就地构建
- 新增 `.gitattributes`：`internal/web/testdata/` 参照文件固定为 LF，
  Windows 检出（`core.autocrlf=true`）不再把 golden 转成 CRLF 导致测试失败

### Removed

- `WORKSPACE_VITE_DEV_URL` / `CONSOLE_VITE_DEV_URL` 以及 DEBUG 模式下的页面注入分支
  （连同对应的参照测试与 3 个 golden 文件）；环境里仍残留这两个变量会拒绝启动

## [0.0.1] - 2026-10-09

### Changed

- Go 重写、单容器、单卷、SQLite；全新基线，不兼容 1.x 数据结构
- 后端由 Django + Postgres + 独立 worker 三服务合并为单进程单二进制
- 前端两个包合并为一个包，构建产物路径保持不变

### Removed

- 移除 MinIO 双存储分支、腾讯云 SES、Postgres 与 Celery 时代遗留脚本
- 移除内部更新推送接口与 RELEASE_PUSH_* 配置
- 移除 AUTO_* 启动开关、GENERATION_TASKS_RUN_INLINE 等运行模式开关
- 移除 django.contrib.admin 与不可达模板
