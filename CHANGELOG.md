# 更新日志

本文件是所有版本信息的单一来源：应用内的「更新说明」弹窗由服务端在启动时解析本文件生成，
`scripts/release.py` 也依赖本文件判断目标版本是否已归档。

## [Unreleased]

### Changed（前端重构 refactor/frontend-ui）

- 前端整体重写落地（0.0.2 前端重构）：单一设计系统 `frontend/shared/design/tokens.css`
  （明暗两套 CSS 变量，SPA 与 Go SSR 页面同源消费）、共享层 `frontend/shared`
  （ui 原语 / hooks / 类型安全 i18n / services / 显式模式契约 / URL 导航）；
  管理台 1196 行单文件拆为壳层 + 五个视图（概览/用户/任务/渠道/设置），
  恢复渠道增删改、失败日志、用户配额详情等能力；管理台与创作台支持
  亮/暗/跟随系统三态主题与中英文切换（语言偏好持久化）。
- 创作台侧栏与顶栏按「白底静奢极简」设计语言重做；移动端底部导航改为横滑
  胶囊布局（修复 375px 挤压）；头像改为本地 identicon 生成，移除 dicebear
  外链；板块与任务回跳接入 URL 状态（刷新/前进后退/分享链接可恢复）。
- 构建管线：`npm run build` 末尾新增产物契约校验（4 个固定产物缺失即失败）
  与 gzip 体积表；线上入口包从约 600KB 降到 554KB（gzip 166KB），Comfy/TTS/
  对话等本地专用代码拆为独立 chunk 不进线上包；删除 0 引用死依赖
  （@google/genai / express / dotenv / tsx / @types/express）；新增
  push/PR 前端质量门禁（lint + build）。

### Fixed

- 本地模式登出不再跳转不存在的登录页（死路径修复）；线上从管理台登出可回跳。
- 三个生成工作台轮询护栏统一（上限 900 次 ≈ 37.5 分钟，云端慢任务不再被提前放弃）。
- 切换板块时的短暂叠影（AnimatePresence mode="wait"）。
- 会话过期（401）统一处理：线上弹登录框不整页刷新，本地模式仅报错。
- 设置/登录/Comfy/TTS 弹窗补齐无障碍行为（Esc 关闭、焦点圈定、滚动锁定）。
- 「下载」不再跳转到浏览器播放器页：本地服务 `/downloads/{token}` 现在认 `?download=1`
  （与线上形状 `disposition=attachment` 等价）并按 `attachment` 下发，媒体表补齐音频扩展名
  （`audio/wav` 等；旧记录里无扩展名的 `.bin` 也按 mime 修正）；TTS 结果与历史记录的下载
  改走 `<a download>` / 临时链接直接落盘，不再出现整页播放器与 `.bin` 后缀。
- TTS 音色库路径与 CLI 对齐：本地服务此前读 `E:\AI\TTS\voices`（不存在），
  实际资产在 `E:\AI\资产\TTS\音色库`（`tts_cli.py` 的 `ASSETS = TTS_ROOT/../资产`），
  导致工作台音色库恒为空、克隆类预热也拿不到参考音频；现已按同一规则解析。

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
- 显卡锁冲突弹窗（生图/TTS 共用）：提交前发现显卡被另一类任务占着时，工作台先弹窗说明
  占用者与已占用时长，由用户选择「等它完成」或「终止对方并继续」；
  `POST /api/gpu/terminate` 负责终止——TTS = 取消任务并停实例释放显存（契约没有中断接口，
  只有停 worker 才能真的停下推理），Comfy = 取消任务（队列删除 + `/interrupt`），
  终止后最多等 10 秒锁释放再继续提交；Comfy 轮询新增 `shouldAbort`，
  任务被取消/终止后立刻收尾，不再干等到超时还占着锁。
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

- TTS 调用层提速与「预加载 / 热态复用」（本地模式 `scripts/localapi/tts.mjs` + 工作台）：
  状态探测由 `list` + `status` 两次 Python 进程合并为一次 `list`；
  工作台顶栏新增「预加载」，`POST /api/tts/start` 加载后自动跑一条极短合成预热——
  实测模型加载 9 秒（冷启动约 50 秒）后首次合成要 21 秒、之后同样文本只要 5 秒，
  多出的约 16 秒是实例内首次推理的一次性开销，现在被挪到预加载阶段；
  常驻改为保活式切换：实例已在跑时不重启（不重付 8~70 秒加载、不丢热态），
  由服务端定期 `start` 顶住空闲超时，`keep_alive` 随 `/api/tts/status` 上报，
  「释放内存」/「停止实例」会一并停掉保活。
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
