# FRONTEND-REFACTOR-PROMPT.md — 前端完整重写提示词

用法：把「提示词正文」整段发给编码 Agent（ZCode / Claude Code / Codex 均可），
在本仓库根目录执行。提示词已内联全部必要背景，Agent 不需要你补充上下文。

---

## 提示词正文（复制以下全部内容）

你是一位资深前端架构师兼视觉/动效工程师。请在 **mirror-studio** 仓库（当前工作目录）中**完整重写**
这套前端：创作台 workspace、管理台 console，以及 Go 渲染的 SSR 页面，全部由你重新组织与实现。

终态标准：一个产品感统一的界面体系 + 一套可维护的代码。此后任何前端改动只改一处，
线上容器版与本地开发版同时生效；信息架构清楚，不再有按技术引擎硬切的板块、不再有字符串前缀路由。

请先通读全部内容再动手。不要向我提问，遇到取舍按本文档的默认建议执行，并在最终报告里写明取舍。

## 0. 授权与自由度（先读这一节）

这次不是增量重构，而是**前端的完整重写**：你被授权推翻现有的文件结构、组件划分、状态管理方式、
路由方案、样式组织方式与信息架构，用自己的方案重建。除 §二 的少数红线外，
本文档里所有的「建议 / 推荐 / 推荐落点」都只是参考，不是限制。

明确可以自由决定（不限于此列表）：

- **目录结构与文件组织**：现有 `components/`、`services/`、`i18n/` 的划分可以全部重排、重命名、废弃；
- **应用形态**：两个 SPA 的边界可以重划（合并成一个应用 + 两套路由区，或拆成更多包），
  只要「管理台」与「创作台」仍是两个可独立访问的入口；
- **组件体系**：自建 UI 原语还是引库（§二.3 的条件）、组件粒度与命名，全部自定；
- **数据层**：`services/apiService.ts` 这个 1390 行单文件可以整体废弃，按你自己的分层重建；
  状态方案（Context / 手写 store / 小库）自选；
- **路由与 URL 模型、样式方案、依赖选型**：分别见 §3.5、§3.1、§二.3；
- **现有代码的处置**：允许删除任何前端文件（三个工作台、两份 i18n、两份 Toast、历史组件……），
  只要 §3.0 的覆盖面自查没有缺口。

唯一要保证的是：**能力一条不少、两种运行形态都可用、风格与产品感统一、质量底线达标。**

# 一、已核实的事实（直接采信，不要重新臆测）

> 这一章描述的是**现状与契约**，不是要你保留的结构。它的用途是让你知道哪些行为必须继续成立、
> 哪些坑前人已经踩过。读完可以把这些文件从地图上删掉，但不要删掉它们承载的能力。

## 1.1 技术栈与包结构

- React 19 + TypeScript ~5.8 + Vite ^6.2；Tailwind CSS 4（SPA，经 `@tailwindcss/vite`）；
  动效库是 `motion` ^12（`motion/react`，已被 17 个组件使用）；图标 `lucide-react`；
  Markdown 渲染 `react-markdown` + `remark-gfm` + `react-syntax-highlighter`。
- **单一 npm 包**：仓库根 `package.json` 是唯一的依赖清单与构建脚本，`frontend/*` 下没有 package.json。
- **没有路由库，没有状态管理库**。导航是 `frontend/workspace/src/App.tsx` 里的 `useState` 切 tab；
  持久化靠 `localStorage`（`mirror.*` 前缀）。
- 两个 SPA：`frontend/workspace`（创作台）、`frontend/console`（管理台）；
  共用 Vite 配置工厂 `frontend/shared/vite.config.base.ts` 的 `createSpaConfig()`；
  类型检查是 `frontend/tsconfig.json` 单一工程（`npm run lint` = `tsc --noEmit`）。

## 1.2 三种运行形态（前端源码同一份，差别只在“外壳 + 可用接口”）

1. **线上容器（Go 单二进制，端口 8000）**：Go 用 SSR 模板渲染外壳
   （`internal/web/templates/workspace/index.html`、`console/index.html`），在 `#workspace-root` /
   `#console-root` 上注入 `data-user`、`data-csrf`、`data-authenticated`、`data-captcha`、品牌名；
   SPA 资源被 Go 以固定路径引用：`/static/workspace/assets/workspace.{js,css}?v=<缓存戳>`、
   `/static/console/assets/console.{js,css}?v=<缓存戳>`，产物经 `go:embed all:static` 打进二进制。
   另有 SSR 页面（`/login`、`/register`、`/account/*`）走 Tailwind **v3** 管线
   （`static_src/css/tailwind.css` → `static/css/main.css`，配置在根 `tailwind.config.js`）。
2. **本地开发者模式（`npm run local`，单端口 8000）**：Node 零依赖本地 API（`scripts/localapi/`）
   + Vite dev server 以 middleware 挂在同一 HTTP 服务上（`scripts/localapi/web.mjs`）。
   `/`、`/workspace` → 工作台 SPA；`/admin`、`/console` → 管理台 SPA；`/api/*`、`/console/api/*`、
   `/downloads/*`、`/healthz`、`/__local` → 本地 API；其余交给 Vite（含 HMR WebSocket）。
   开发入口 `frontend/workspace/index.html` 带 `data-mode="local"`、`data-authenticated="1"`。
3. **分离开发（`npm run dev:workspace` :3000 / `dev:console` :3001）**：把 `/api`、`/console/api`、
   `/downloads` 代理到 `LOCAL_API_TARGET`（默认 `http://127.0.0.1:8000`），可对接本地 API 或
   `DEBUG=1 go run .`。

## 1.3 唯一的模式开关（现状）

`document.getElementById("workspace-root")?.dataset.mode === "local"`，目前散落在：

- `frontend/workspace/src/App.tsx:74-75`（`comfyEnabled` / `ttsEnabled`）
- `frontend/workspace/src/components/TopToolbar.tsx:64-65`（对话中转站配置入口）

线上 Go 模板**不带** `data-mode`，靠“没有这个属性”表达 online。
`/api/comfy/*`、`/api/tts/*`、`/api/chat/config|stream` 只有本地 API 实现，Go 后端没有这些路由。
`docs/LOCAL-MODE.md:51-52` 记录着既有契约：两个前端应用对后端**不需要任何条件分支**（URL 形状一致）。

## 1.4 事实上的“两套前端”（本次要合并的东西）

- **样式两套**：SPA 用 Tailwind v4，`@theme` 只定义了 4 个颜色 token
  （`frontend/workspace/src/index.css:4-12`）；SSR 页用 Tailwind v3 + 另一套调色/圆角/玻璃拟态
  （`tailwind.config.js`、`static_src/css/tailwind.css`、`internal/web/templates/base.html` 里的
  `ArtMirror Design Language Override` 内联样式）。两套无共享 token。
  且 `tailwind.config.js` 的 `content` 还指向早已不存在的 `templates/`、`apps/` 目录（Django 时代残留）。
- **重复副本**：`frontend/workspace/src/index.css`(115 行) 与 `frontend/console/src/index.css`(135 行)
  约 80% 重复；两份 i18n 字典（`workspace/src/i18n/index.ts` 292 行 / `console/src/i18n/index.ts` 280 行）
  近似重复且已开始漂移；两份 `components/Toast.tsx`（各 65 行）。
- **逻辑重复**：`GenerationWorkbench.tsx`(1567 行) / `ComfyWorkbench.tsx`(1827 行) /
  `TtsWorkbench.tsx`(1582 行) 各自实现一套「上传 → 轮询 → 断点恢复」，常量都不一致：
  `MAX_POLL_ATTEMPTS` 在 Generation 是 50（`GenerationWorkbench.tsx:83`），在 Comfy/TTS 是 900
  （`ComfyWorkbench.tsx:94`、`TtsWorkbench.tsx:61`）。`services/apiService.ts` 1390 行，
  把 auth / 生成 / 分片上传 / 小说 / 对话 / Comfy / TTS 混在一个文件（console 侧那份 330 行）。
- **线上产物背着本地专用代码**：`App.tsx` 顶部静态 import 了 `ChatWidget`、`ComfyWorkbench`、
  `TtsWorkbench`，线上永远用不到却照样进包（当前 workspace 产物约 600KB JS）。
- **console 单文件**：`frontend/console/src/App.tsx` 1196 行，5 个 tab 全内联，且完全没用 `motion`。
- **无暗色模式**：全仓 0 个 `dark:` 用法，但 SSR 外壳已声明 `data-theme="light" data-theme-mode="system"`。
- **字体是外部阻塞依赖**：两个 `index.css` 第 1 行都是 `@import url('https://fonts.googleapis.com/...')`，
  离线/受限网络下首屏字体必挂。
- **无错误边界**：全仓没有 ErrorBoundary；无骨架屏/空态/错误态的统一样式。

## 1.5 构建与产物契约（冻结，不得破坏）

- `npm run build` = `build:css`（v3 CLI）→ `build:workspace` → `build:console` → `build:ssr`（空操作）。
- 产物文件名固定：`assets/workspace.js`、`assets/workspace.css`、`assets/console.js`、
  `assets/console.css`（见 `frontend/shared/vite.config.base.ts:51-61`）。
- `static/` 是唯一构建产物目录（被 go:embed 打包）；`static/workspace/`、`static/console/`、
  `static/css/main.css` 在 `.gitignore` 里，`static/` 下其它文件（favicon、images）是入库的。
- Dockerfile 只在 stage 1 构建前端，**运行容器内没有 node**；新前端必须构建期完全产出。
- 版本号单一来源是根 `VERSION`；`frontend/shared/app-version.mjs` 把它注入 `VITE_APP_VERSION`
  （`npm run local` 的 dev server 也调用同一实现，见 `scripts/localapi/web.mjs:21`）。
- `docs/FRONTEND.md:37-38` 有一条 0.0.1 时期的旧约束：「SPA 运行时渲染输出逐字节一致」。
  **本次重构明确解除该约束**（要改 UI 就必须解除）；但 Go 侧资源路径、文件名、`?v=` 缓存戳冻结。

## 1.6 跳转与导航现状（要重写的部分）

- **没有路由**：板块切换是 `App.tsx` 的 `useState`；刷新、前进后退都不保留位置，也没有可分享的深链接。
- **三处 `window.location.href` 整页跳转**：
  - 登出后无条件跳 `/workspace?auth=login`（`apiService.ts:385-391`）——本地模式根本没有登录页，这是死路径；
  - 401 时 `apiRequest` 抛 `AuthRequiredError` 后整页重定向到 `/workspace?auth=login`（`apiService.ts:225-230`），
    丢掉当前页面的全部状态；
  - 登录成功后按 `next` 参数整页跳转（`App.tsx:252-254`）。
- **URL 只被当成一次性指令通道**：`?auth=login|register` 与 `?next=` 在 `App.tsx:206-219` 解析后立刻
  `history.replaceState` 清掉，URL 不是状态来源。
- **两端策略各写一份**：console 有独立的 401 跳转（`/workspace?auth=login&next=/console`，
  `console/src/services/apiService.ts:226-229`），与 workspace 不一致。
- **回跳靠字符串前缀**：历史 → 编辑器靠 task_no 前缀（`comfy:` / `tts:` / `novelviz:`）在
  `App.tsx:97-128` 手工分发。
- **模式差异**：本地没有登录/注册页（固定 local 账号）；线上有 SSR 的 `/login`、`/register`、
  `/account/*`。同一套跳转策略必须同时覆盖两种，并且本地模式不该跳登录页。

## 1.7 构建与开发管线现状（要重写的部分）

- **一条命令四步**：`npm run build` = `build:css`（Tailwind v3 CLI → SSR 的 `static/css/main.css`）→
  `build:workspace` → `build:console` → `build:ssr`（`echo` 空操作，纯说明）。
- **两条样式管线并存**（v3 / v4），token 不共享；根 `tailwind.config.js` 的 `content` 指向已不存在的
  `templates/`、`apps/` 目录。
- **SPA 产物契约**：Vite 配置没有 `base`；产物固定名 `assets/workspace.js|css`、`assets/console.js|css`；
  Go 以固定路径 + `?v=` 缓存戳引用；`static/` 由 `go:embed all:static` 打包进二进制。
- **Dockerfile**：stage 1 只带 `package*.json` + `frontend/{shared,workspace,console}` +
  `frontend/tsconfig.json` + `static_src/` + 根 postcss/tailwind 配置来 `npm ci && npm run build`，
  把 `static/` 交给 Go 构建阶段；运行镜像内**没有 node**。
- **本地 dev 接线是第三套实现**：`scripts/localapi/web.mjs` 自建 Vite dev server
  （`transformIndexHtml` 重写 `/src/...` 前缀、HMR 复用同一端口、`/` `/workspace` `/admin` 路径映射、
  未知无扩展名路径回退到工作台外壳）。
- **CI 无质量门禁**：`.github/workflows/container.yml` 只在打 `v*` tag 时构建推送镜像，没有 lint/build 检查。
- **死依赖**：根 `package.json` 的 `@google/genai`、`express`、`dotenv`、`tsx`、`@types/express`
  已无源码引用（0.0.1 为 lockfile 逐字节稳定而保留，约定 0.0.2 清理）。

# 二、红线（只有这些不可协商）

以下每一条都属于「改错了产品直接坏」的契约级别；除此以外的一切都可以推翻重做。

1. 前端整体重写（删除、重建、重排任何前端文件都允许）；Go 侧只允许两类改动：
   （a）在 SSR 工作台/管理台模板上补**显式**的 `data-mode="online"`（见 §3.2）；
   （b）同步更新 SSR 模板/`static_src` 的类名与结构以匹配新设计系统。
   **两个应用的信息架构可以自由重写**：菜单/导航结构、板块划分、页面布局、组件实现都可以推倒重来
   （包括重做 `frontend/console/src/App.tsx` 这个 1196 行单文件、合并或新增板块、改菜单文案与层级），
   不需要保留现有菜单项。唯一硬约束是它们必须看起来是同一个产品（§4.0 风格契约）。
   业务行为与接口调用语义仍然不变（§二.2、§十）。
2. 不改 `scripts/localapi/` 的接口契约（URL、字段名、状态机）。若发现本地与线上接口有真实分歧
   （例如本地 `/downloads/{token}` vs Go `/downloads/{token}/`），在报告中列出，前端按两边兼容处理并验证。
3. 依赖策略：**不做禁令**。若引入能显著降低自研成本与缺陷面（路由、无样式无障碍原语、
   虚拟列表、图表、状态容器），可以用，条件只有三条：
   （a）报告里给出 gzip 体积增量与「手写 vs 引库」的取舍理由；
   （b）不得引入第二套设计语言（禁止带样式的整套组件库：MUI / AntD / Element / Bootstrap 等）；
   （c）不新增运行时外链（字体 / 图标 / 埋点 CDN 依旧禁止）。
   当前入口包约 600KB，小库的预算不成问题，但要用体积数据说话。
4. 产物路径契约冻结：`/static/workspace/assets/workspace.js|css`、
   `/static/console/assets/console.js|css` 名字不变；`npm run build` 仍然一条命令产出全部。
5. **双模式都必须可用**：改完必须能在 `npm run local` 与 `DEBUG=1 go run .`（或容器形态）下正常工作。
   本地专用入口（Comfy / TTS / 对话悬浮窗）在线上形态下必须既不可见、也不发任何请求、且代码不加载。
6. i18n 必须 zh/en 双语完整；不允许新增只有中文的硬编码文案（引擎名、日志原文等专有名词除外）。
7. `npm run lint`、`npm run build` 必须全绿；`npm run local:check` 必须全过。
8. 无障碍基线：图标按钮有 `aria-label`，弹窗有 `role="dialog"` + 焦点圈定 + Esc 关闭，
   全站尊重 `prefers-reduced-motion`，键盘可完成主流程。
9. 不擅自改 `VERSION`；CHANGELOG 按 `docs/RELEASING.md` 的口径追加。

# 三、结构重设计：信息架构 + 合并两套前端（任务核心）

## 3.0 信息架构重设计（明确允许，且是本次重点之一）

现状的问题（已确认，可以直接当结论用）：一级导航是按**技术引擎**而不是按**用户任务**切的——
创作台 / Comfy 生图 / TTS 语音合成 / 小说可视化 / 历史并列成五个一级板块，其中前四个本质都是
「提交一个生成任务」，只是引擎与产物不同；历史记录靠 task_no 前缀（`comfy:` / `tts:` / `novelviz:`）
反查该跳回哪个板块，等于把 IA 问题编码进了数据；管理台则是 5 个 tab 内联在一个 1196 行文件里，
分组粒度同样可疑。这一层被授权彻底重做。

重设计原则（方向自选，结论不预设）：
1. 一级导航按用户任务/心智分组，不按技术引擎分组；同一个「生成」动作的不同引擎应作为同一页面内的
   类型切换，而不是并列的一级入口。
2. 层级不超过两层（一级板块 + 页内分区 / 抽屉）；超过两层通常说明分类失败。
3. 术语统一：板块名、任务类型名、历史筛选名、按钮文案指同一件事时必须用同一个词（中英各一套）。
4. 历史与结果视图应当跨引擎统一（按类型/状态筛选），点开能回到对应编辑器恢复上下文，
   不依赖字符串前缀路由。
5. 管理台按「管理对象」分组（例如 概览 / 资源 / 运营 / 系统 这类），重操作（渠道编辑、配额调整）
   进抽屉或独立页，不塞进 tab。
6. 允许合并、拆分、更名、升降级入口——包括把 Comfy / TTS 从一级板块降级为生成页里的引擎选项；
   也允许反其道而行，只要理由写清楚。**唯一不能丢的是能力**。

覆盖面自查（硬要求）：新 IA 必须覆盖现有全部能力与入口，逐条对照打勾——文生图、图生图、
参考图上传、小说可视化（EPUB → 场景分析 → 场景图）、历史与任务恢复/取消/删除、
Comfy 生图（含设置 / 启动 / 显存）、TTS 四种模式 + 音色库 + 引擎实例管理、
对话悬浮窗与「填回描述」、账号与额度设置、发布说明、管理台全部功能（用户 / 任务 / 渠道 /
设置 / 配额 / 日志）。

URL 形态：可以自由定义，但要先想清楚可达性——不改 Go 路由时，只能用 `/workspace`、`/console`、
`/admin` 内部的 query 或 hash；如果确实需要真实子路径（例如 `/workspace/create`），
必须在报告里列出所需的 Go 路由改动与向后兼容方案（旧路径仍可达），不要悄悄加路径后线上 404。

IA 变更的配套产出：新的导航树（写进 `docs/FRONTEND.md`）、「旧入口 → 新入口」对照表、
以及每个一级板块「为什么这样分」的一句话理由。归类有争议时，选对新手更直观的方案并写明取舍。

## 3.1 单一设计系统

新建**唯一 token 来源**（落点与文件名自选，建议放在两个应用都引得到的地方）：纯 CSS 变量，`:root` 与
`[data-theme="dark"]` 两组），要求：

- SPA 侧：各应用的 `index.css` 里用 Tailwind v4 的 `@theme` 引用这些变量（`--color-*` 映射），
  不允许再新增硬编码色值。
- SSR 侧：`tailwind.config.js` 与 `static_src/css/tailwind.css` 也只引用同名变量；
  `internal/web/templates/base.html` 里的内联 `ArtMirror Design Language Override` 合并进
  `static_src/css/tailwind.css`（用 `@layer components` 表达），模板里只留必要的结构类。
- 之后改一次颜色/圆角/阴影/字体，SPA 与 Go SSR 页面同步生效。
- 消费方式自由（继续用现在的 Tailwind v4 / v3 双管线，或换成你重写后的方案都行）；
  唯一要求是两边都只从这一处取值，且「改一处、两边同时生效」可被验证。

## 3.2 显式模式契约（不再用“属性缺失”表达 online）

- 新建一个**唯一**的模式/能力模块（落点建议 `frontend/shared/config/app-mode.ts`，也可以自选路径；
  或放 shared 由两应用共用）作为**唯一**模式/能力来源，只读一次 DOM，导出 `AppConfig`：
  `{ mode: "local" | "online"; features: { comfy, tts, chatWidget, localAccounts, ... } }`。
- 线上 Go 模板补 `data-mode="online"`，本地 `index.html` 保留 `data-mode="local"`；
  缺失时默认按 `"online"` 处理。
- 删除散落的 `dataset.mode` 读取（`App.tsx`、`TopToolbar.tsx`），全部改走 `appConfig`。
- 侧栏/路由/工具栏由**声明式功能清单**驱动（一个数组 + feature flag），本地与线上共享同一条渲染代码路径。

## 3.3 逻辑去重（每类只留一份）

- `frontend/shared/` 下新增（路径可微调，但必须两应用共用）：
  - `ui/`：Button、IconButton、Card、Panel、Modal/Dialog、Drawer、Input、Select、
    SegmentedControl、Tooltip、Badge、Skeleton、EmptyState、Spinner、ProgressBar/Ring、Tabs、Table、Pagination、Switch。
  - `hooks/`：`useTaskPolling`（统一轮询/重试/退避/断点恢复/卸载清理）、`useChunkedUpload`、
    `useLocalStorage`、`useMediaQuery`、`useTheme`、`useToast`、`useDebounce`。
  - `i18n/`：合并两份字典为一份（按 `common / workspace / console` 分命名空间），
    导出 `zh`/`en` 与**类型安全**的 key（消灭 `t: any`，见 `Sidebar.tsx:28` 附近）。
  - `Toast`：一份，两应用复用。
- 生成类逻辑必须只有一份实现：无论新 IA 是否把三个工作台合并，上传 / 轮询 / 断点恢复都只能有一套
  （`useTaskPolling` / `useChunkedUpload` 或等价设计），并统一 `MAX_POLL_ATTEMPTS` 等常量
  （Generation 的 50 与 Comfy/TTS 的 900 不一致，先确认哪个是正确语义再统一，并写进代码注释）。
- `apiService.ts` 按域拆分（`auth / generation / upload / novelviz / chat / comfy / tts`），
  保留现有 `apiRequest` 信封与 401/CSRF 重试语义（`apiService.ts:210-252`）。

## 3.4 按能力拆包（同时解决“线上背着本地代码”与体积）

- 本地专用能力（Comfy / TTS / 对话，以及任何只在本地模式出现的新功能）**不得进入线上产物**：
  线上包不能包含这些代码。实现手法自选（路由级懒加载、动态 import、构建期别名替换、独立入口都行），
  但要在报告里说明手法，并验证线上形态下 Network 面板确实不加载这些 chunk。原方案（可参考）：
  `React.lazy` + `Suspense`（骨架屏兜底），只在对应 feature 开启时才 import。
- **前提**（必须先验证）：`frontend/shared/vite.config.base.ts` 目前没设 `base`。
  一旦出现动态 chunk 或经打包的静态资源（字体/图片），必须先确认产物引用路径在
  `/static/<app>/assets/` 下正确：建议 `base: command === 'build' ? '/static/<app>/' : '/'`
  （dev 保持 `/`，否则 `npm run local` 与 `dev:*` 的 URL 会错位），并实测线上形态下
  chunk 能加载、CSS 里 `url()` 指向正确。若结论是不需要改 `base`，在报告里写明验证方法。
- 若新增自托管字体/图片：优先让它们同时可在本地模式访问。**推荐**在
  `scripts/localapi/web.mjs` 里加一个 `/static/**` 直通仓库 `static/` 目录的只读分支
  （仅基础设施改动，不动接口契约），这样 `/static/fonts/*` 在三种形态下地址一致。

## 3.5 跳转与导航逻辑（整段重写）

目标：把"散落各处的 `window.location` 赋值 + 一次性 query 指令"重写成**一套唯一的导航策略**。

1. **单一导航入口**：板块切换、弹窗开关、任务回跳、跨应用跳转都走同一个导航层（自研小路由或引入库，
   按 §二.3 的条件自行决定）。除"确实需要整页重载"的极少数场景外，禁止在组件里直接写
   `window.location.href = ...`；例外必须在报告里逐条列出并说明原因。
2. **URL 是状态来源**：一级板块、选中的任务、打开的弹窗（至少 auth / settings）都能由 URL 恢复；
   刷新、前进后退、复制链接分享都要正确。
3. **鉴权跳转集中化**：401 / 未登录 / 权限不足只有一处策略，且能感知模式——
   线上：未登录 → 打开登录并保留 `next`，回跳后恢复到原位置（不再整页刷新丢状态）；
   本地：没有登录概念，401 只提示错误、绝不跳登录页（消灭 `apiService.ts:385-391` 那条死路径）；
   workspace 与 console 共用这一处策略，不再各写一份。
4. **任务回跳结构化**：历史 → 编辑器/详情用「类型 + 任务号」的结构化参数表达；
   旧的 `comfy:` / `tts:` / `novelviz:` 前缀仅作为兼容输入解析，不再作为内部路由机制。
5. **跨应用跳转统一**：workspace ↔ console 走同一个 helper，带 `next` 且能回到原位置。
6. **站内零整页刷新**：客户端完成跳转（退出登录、需要重新注入 SSR 外壳等必要场景例外，逐条列明）。
7. **向后兼容**：旧的 `?auth=login|register`、`?next=`、`/console`、`/admin` 链接必须继续可用；
   URL 形态受 §3.0 的可达性约束（不改 Go 路由时只能用 query/hash）。
8. **覆盖清单**（逐条实测）：刷新保留位置；后退/前进；旧 `?auth=` 链接仍能唤起登录；登录后回跳到原位置；
   会话过期（401）的提示与恢复；本地模式登出；历史点开三类任务；workspace ↔ console 互跳；移动端返回键。

## 3.6 构建与开发管线（整段重写）

目标：两条样式管线归一，四个脚本变成一条可解释、可校验的管线。

1. **脚本收敛**：允许重命名与合并（建议按 `build / dev / check` 三类组织），但必须：
   (a) `npm run build` 一条命令产出全部产物；(b) 保留 `local`、`local:check`、`lint` 这些既有入口名
   （改名的要留别名并同步 `docs/`）；(c) 每个脚本在 `docs/FRONTEND.md` 里用一句话说明它做什么。
2. **SSR 样式管线二选一**：(a) 把 `static_src/css/tailwind.css` 迁到 Tailwind v4，与 SPA 同版本、
   同 token 来源；或 (b) 保留 v3 CLI 但让它只消费共享 token 文件。无论选哪个，都必须修正
   `tailwind.config.js` 的 `content` glob、删掉重复配置，且 SSR 与 SPA 的样式同源可被验证。
3. **Vite 配置**：完成 §3.4 的 `base` 问题；`createSpaConfig` 可以扩展，但必须保持"两应用配置唯一来源"；
   产物文件名的契约用注释 + 构建期断言保护。
4. **构建期校验（新增）**：build 结束后输出体积表（各应用 js/css，gzip 前后），检查 4 个契约产物文件名
   存在——缺失直接 fail，不要静默产出坏包。
5. **死依赖清理**：删掉无引用的 `@google/genai` / `express` / `dotenv` / `tsx` / `@types/express`，
   并验证 `npm ci` 与 `docker build` 仍通过；若证明删除会破坏 lockfile 稳定性，写清原因再保留。
6. **开发-生产一致性**：`scripts/localapi/web.mjs` 的 dev 接线尽量与生产共用同一套资源解析
   （同上文的 `/static/**` 直通、同一套 HTML 变换），减少"本地能跑、线上白屏"这一类问题的土壤。
7. CI 的质量门禁见 §六 W6。

# 四、设计语言、产品感与「高级感」规范（必须落到 token，不靠感觉）

## 4.0 风格契约：先把「那种风格」写成可执行的清单

现有 UI 的风格可以定名为**「白底静奢极简」（Quiet Minimal Light）**——苹果 HIG / Linear / Vercel
那一挂的克制路线：极浅底 + 纯白卡片 + 近黑文字 + 单一强调色 + 大圆角 + 极浅边框 + 轻阴影 +
弹簧微动效。这次要做的是**在这个路线上做得更纯、更统一**，不是换一种风格。

**九条识别特征（同时成立才算这个风格）：**

1. 底色是极浅冷灰（#F7F8FB ~ #F9FAFB），内容承载在纯白卡片上；深色只给文字与”主要动作”。
2. 文字三级：近黑 #1A1A1A / 中灰 #6B7280 / 弱灰 #9CA3AF；不用纯黑，不用彩色正文。
3. 全站只有 **1 个强调色**（沿用已在用的 #6D5DFC），只出现在”新建 / 主行动”等极少数位置；
   其余颜色一律语义化（成功 / 警告 / 错误 / 信息）。
4. 圆角大而统一：卡片 16–24、控件 10–12、胶囊 999；同一层级不允许混用不同档。
5. 结构靠极浅边框（#E5E7EB 级别）表达；阴影只做”浮起感”，不做”厚重感”。
6. 玻璃拟态（backdrop-blur）只给常驻 chrome（顶栏、侧栏、悬浮层），卡片一律不用。
7. 节奏靠”小号 + 大写 + 宽字距”的微标签（11px uppercase tracking-widest）与大量留白；
   标题 tracking-tight；数字 tabular-nums。不靠粗体和大字号堆气势。
8. 动效是弹簧 + 短促淡入（120–360ms）；选中态用共享元素滑动（layoutId）表达，而不是换背景色。
9. 图标只用一套线性图标（lucide-react，统一 stroke-width 与尺寸档）；不用 emoji、不用插画贴纸。

**反面清单（出现任何一条就跑偏）：** 多彩/霓虹渐变、大色块填充、重投影、饱和色大面积背景、
暗色赛博风、装饰性插画、圆角随机混用、动效超过 400ms 或带夸张的旋转/缩放入场。

**两个应用的关系：** workspace（创作台）与 console（管理台）必须看起来是同一个产品的两个区域——
共用同一套 token、同一套基础组件、同一套动效参数；只允许信息密度与操作类型不同（管理台可以更
“表格化”、更密），但不允许换配色、换圆角、换字体、换图标体系。

在这个契约之上，把现有的 4 个 token 扩充成完整体系：

- **色彩**：中性色阶（语义命名 `ink / surface / border / muted`，替换散落的 `gray-*` 调色板用法）；
  一个强调色（沿用界面里已出现的 `#6d5dfc` 作为 accent，统一 500/600 两档）；
  语义色 success / warning / danger / info。全部在 tokens 里定义一次，明暗两套。
- **圆角阶**：8 / 12 / 16 / 24 / 9999，替换当前的随机值（同一层级只允许用同一档）。
- **高度**：三级阴影（raised / floating / overlay）+ 1px 边框；玻璃拟态只用于常驻 chrome
  （顶栏、侧栏、悬浮层），不铺满卡片。
- **排版阶**：页面标题 20/600、区块标题 14/600、正文 14/400、辅助 12/500；标题 `tracking-tight`；
  所有数字（额度、进度、耗时）用 `tabular-nums`。
- **字体**：删除两个 `index.css` 的 Google Fonts 阻塞式 `@import`。默认改为系统字体栈
  （`-apple-system, "Segoe UI", system-ui, "PingFang SC", "Microsoft YaHei", "Noto Sans SC", sans-serif`），
  等宽用 `ui-monospace, "JetBrains Mono", Consolas, monospace`。自托管 Inter 属可选加分项，
  按 §3.4 的资源规则做，且必须验证三种形态都能命中。
- **暗色模式**：支持 `data-theme="dark"`、`prefers-color-scheme` 跟随、用户显式三态
  （light / dark / system）持久化到 `localStorage("mirror.theme")`；入口放在 TopToolbar 与设置弹窗；
  SSR 页面同步支持（外壳已带 `data-theme` / `data-theme-mode` 属性，正好接上）。
  验收：明暗两套下无对比度不足、无“白底黑字漏色”的组件。
- **页面级细节（“网站感”）**：`document.title` 随板块切换；滚动位置在切 tab 后可恢复；
  骨架屏与最终布局同尺寸（避免 CLS）；生成完成的结果图 `fade-in + 轻微 blur-up`；
  空态/错误态有统一插画位与一句话引导；输入框/按钮 hover/focus/pressed 三态齐全且键盘可见 focus ring。

## 4.1 产品级标准：要像一个真实产品，不能像个玩具

这一节是本次重写的**核心质量门槛**。风格再好看，只要下面这些没做到，成品依然是玩具。
逐条实现，并在报告里给出证据。

**1. 每个异步界面都有完整状态。** 不是只有成功路径。列表 / 工作台 / 详情至少五态：
加载（骨架屏，与最终布局同尺寸，不跳变）、空（一句话说清为什么空 + 一个下一步动作）、
错误（说清发生了什么 + 重试入口）、部分成功（部分失败时保留成功项并标出失败项）、
失效（任务被清理、链接过期）。禁止：整页居中转圈、一片空白、只在控制台报错。

**2. 没有死路。** 每个错误态与空态都要有下一步动作（重试 / 返回 / 去配置 / 看文档）；
每个页面都有明确的返回路径；不允许出现"用户只能刷新页面"的处境。

**3. 数据连续性：刷新不丢工作。** 用户输入（提示词、参数、上传的参考图、表单）在刷新、
切板块、误触返回后不丢——草稿自动保存并有"已保存"的轻量指示，带未提交内容离开时给保护；
进行中的任务刷新后能自动接续，且这套接续逻辑**全站只有一处实现**。

**4. 每个动作都有即时反馈。** 点击后 100ms 内必须可见（按钮 loading / 禁用 / 乐观更新）；
长任务显示阶段与进度，不是无限转圈；提交类按钮防重复提交；
危险操作（删除、取消任务、切换 TTS 引擎）要确认，且确认文案写清后果。

**5. 文案是产品文案。** 按钮用动词短语（"生成图片"而不是"确定"）；错误信息是
"发生了什么 + 怎么办"，不是"任务失败"四个字或原始异常；空态文案给下一步而不是道歉；
中英文案同源维护，EN 模式下不允许混进中文。

**6. 边界情形必须被设计过。** 超长提示词（5000 字）、超大参考图、上千条历史、
慢网（Slow 3G）、断网中途恢复、并发任务、额度耗尽、空账号、任务取消/失败/超时。
每条要么被优雅处理，要么有明确上限与提示。不允许"没考虑到"。

**7. 细节即产品感。** 时间与数字格式统一（相对时间 + tabular-nums）；列表截断规则一致；
浏览器标题随路由变化；复制 / 下载 / 分享这类次要动作有稳定去处；favicon、404 与错误页
必须与设计系统一致（不能是裸的浏览器默认页）；滚动条、选中色、focus ring 全站统一。

**8. 性能是可见的产品感。** 首屏无布局抖动；板块切换即时（不做无意义的全页 loading）；
图片懒加载 + 占位；构建产物有预算——重写后线上入口 chunk **不得大于当前的约 600KB**，
且不含本地专用代码；限速网络下关键路径仍可用（不依赖外链）。

**9. 零控制台噪音。** 生产路径不出现 console.error / warn、React key 警告、未处理的
Promise rejection、404 请求；开发期日志收尾时清理干净。

**10. 禁止假实现。** 不允许"按钮点了没反应"、TODO 占位、写死的假数据、注释掉的入口进入交付。
还不能工作的入口要么补齐、要么删掉，不要留在界面上。

**自检方式**：把上面 10 条做成走查表，在两种运行形态下逐条实走一遍（§八 有配套的边界情形清单），
报告里给结论与证据（截图 / 录屏 / 命令输出均可）。产品感不是形容词，是这张表全部打勾。

# 五、动效规范（新增，克制的产品级动效）

- 统一用 `motion/react`，在 App 根部包一层 `<MotionConfig reducedMotion="user">`。
- 动效 token：duration 120 / 180 / 260 / 360ms；标准 easing `cubic-bezier(0.2,0,0,1)`，
  强调 easing `cubic-bezier(0.16,1,0.3,1)`；弹簧默认 `{type:"spring", stiffness:300, damping:30}`
  （沿用 `Sidebar.tsx:59` 已有的参数，全站统一）。
- 只动 `transform` 与 `opacity`。`prefers-reduced-motion` 下全部退化为无动画（不是变慢）。
- 清单（至少做到）：
  1) 板块切换：`AnimatePresence mode="wait"` + 6px 位移淡入，避免当前无 `mode` 的叠影（`App.tsx:311`）；
  2) 侧栏选中 pill：保留现有 `layoutId` 弹簧（`Sidebar.tsx:120-124`），并给折叠/展开加统一缓动；
  3) 弹窗：scale 0.96→1 + 背景 blur 淡入，退出反向；打开时锁定滚动、圈定焦点；
  4) Toast：右侧滑入 + 高度/间距弹簧；堆叠上限与自动消失时长统一；
  5) 历史/列表卡片：进入时 stagger（每项 20ms）；过滤切换时 `layout` 平滑重排；
  6) 骨架屏 shimmer；任务进度环/条数值平滑过渡；额度数字滚动（tabular-nums 防抖动）；
  7) 按钮 active `scale: 0.98`；卡片 hover 抬升 1px + 边框加深；图标点击后的微反馈。
- console 应用补齐基础动效（当前完全没有 motion）：tab 切换、表格行进入、弹窗、Toast。
- 清单是**下限不是上限**：可以按 §4.0 的风格契约加更多动效，只要不违反 reduced-motion 与
  「只动 transform/opacity」两条。

# 六、建议节奏（顺序可调整，三条纪律不可省）

下面的阶段划分是**建议**，不是流程强制；你可以合并或重排阶段。但三条纪律必须保留：

1. **体检先于改动**（W0.5）：动手之前先做运行时审查并留下台账——文件一旦被重写或删除，
   很多问题就再也复现不了了；
2. **结构与行为改动分开提交**：修复不与大改动混在同一个 commit，否则出问题无法归因；
3. **收尾必须有独立复核 + 全量回归**（W7）：由另一个上下文做只读复核，专找重写引入的新问题。

每阶段结束都必须 `npm run lint` + `npm run build` 通过再进入下一阶段。

- **W0 基线**：先把当前工作树（含未提交的 TTS/Comfy/Chat 改动）确认并提交；
  从 `feature/local-tts` 切出 `refactor/frontend-ui`。记录基线：`static/workspace/assets/*.js|css`
  与 `static/console/assets/*.js|css` 的体积、`npm run local` 与线上形态的可用性。
- **W0.5 审查体检（必须做，不能跳过）**：按 §7.1 的十个维度做一轮**运行时**审查（不是只读代码），
  产出 `docs/FRONTEND-AUDIT.md` 缺陷台账：每条含现象、复现步骤、影响范围（local / online / 两者）、
  证据（文件:行、控制台报错、截图）、定性（真 bug / 设计如此 / 待定）、优先级
  （P0 阻塞主流程 / P1 本次修 / P2 记录待办）、修复归属阶段。
  §7.2 的候选清单是静态阅读得到的线索，**必须逐条运行时验证后再定性**，验证不成立的条目也要写明。
  审查与实现不要放在同一个上下文里做（建议用独立子代理跑审查），避免"自己审自己"。
- **W1 设计系统**：tokens.css + 两套 Tailwind 接管 + 系统字体栈 + 删除 Google Fonts 依赖；
  SSR 模板内联样式并入 v3 样式表；产出 `docs/FRONTEND.md` 的设计系统小节。
- **W2 信息架构 + 共享层**：先定 IA（§3.0）再搬家，顺序不能反。
  IA：产出两应用各自的「旧入口 → 新入口」对照表与新的导航树，做完覆盖面自查
  （§3.0 能力清单逐条有归属），先写进 `docs/FRONTEND.md` 再开始改代码。
  共享层：`frontend/shared/{ui,hooks,i18n,design}` 落地（路径可调整，但两应用必须只有这一份）；两应用改为引用；
  删除重复的 Toast / i18n / index.css；`MAX_POLL_ATTEMPTS` 等常量统一。
  注意三点：
  (a) 在两个 `index.css` 里用 Tailwind v4 的 `@source` 显式声明共享目录
      （例如 `@source "../../shared";`），否则共享组件里的类名不会生成样式；
  (b) 在 `frontend/tsconfig.json` 的 `paths` 与 Vite `resolve.alias` 里同步加别名
      （建议 `@shared/*` → `frontend/shared/*`），并确认本地模式下 Vite dev server 能读取
      仓库根（必要时设 `server.fs.allow`）；
  (c) 共享组件不得 import 任一应用的私有模块。
- **W3 模式契约 + 导航重写 + 拆包**：
  导航：按 §3.5 重写跳转与 URL 状态（「跳转逻辑重写」的落点，先做，因为 IA 与弹窗都依赖它）；
  模式：`appConfig` + 声明式功能清单 + 显式 `data-mode="online"`；
  本地专用组件改 `React.lazy`（先完成 §3.4 的 base/资源路径验证）；确认线上包不再包含本地代码。
- **W4 视觉与动效**：按 §4（含 §4.0 风格契约）、§5 全面落地（workspace 与 console 两个应用、SSR 页面）。
  允许并鼓励在这一阶段重写两个应用的信息架构与菜单：console 的 1196 行单文件拆成正常的
  「布局 + 页面 + 表格/表单组件」结构，菜单按管理员的心智重排（概览 / 用户 / 任务 / 渠道 / 设置
  这五类可以合并、更名或拆分）；workspace 的侧栏与顶栏也可以重做，但入口必须覆盖现有全部能力
  （生图、Comfy、TTS、小说可视化、历史、对话悬浮窗）。菜单重写后要同步更新 `docs/` 里带截图
  或路径说明的段落，并在报告里给出"旧入口 → 新入口"的对照表。
- **W5 Bug 修复**：处理 §7.2 台账里的 P0/P1。纪律：**先写复现步骤 → 修 → 按原步骤复现验证**；
  修完后在台账里回填结论与提交号。修复提交与结构重构提交分开（同一个 W 内可以先 refactor 再 fix，
  但不允许"顺手改"混进大重构的 commit）。P2 只记录、不动手，写清不做的理由。
- **W6 构建管线 + 性能与健壮性**：先按 §3.6 重写构建/开发管线（脚本收敛、SSR 样式管线二选一、
  构建期体积与契约校验、死依赖清理、dev/prod 一致性）；再做性能与健壮性——拆包体积对比、图片 `loading="lazy"`、错误边界（每个板块一个 ErrorBoundary +
  全局兜底）、网络恢复提示、长列表虚拟化只做评估（不强制）。
  顺带补质量门禁：`.github/workflows` 目前只在打 tag 时构建镜像，没有 lint/build 检查——
  加一个 push/PR 上跑 `npm run lint` + `npm run build` 的 job，让"审查修复"的成果不再回流。
- **W7 收尾 + 回归审查**：按 §八 清单**全量复走一遍**（§4.1 产品级标准 + 双模式 × 桌面/移动 × 明暗 × 键盘 ×
  reduced-motion），并把结果写进台账：每条 P0/P1 标注"已修复且复现验证通过"，或"明确不修 + 理由"。
  再由一个独立子代理做一次只读复核（不看实现过程，只看最终代码与运行结果），专找
  "重构引入的新问题"与台账漏网项。最后更新 `docs/FRONTEND.md`（新结构、新增目录、设计系统、
  双模式契约）、`docs/LOCAL-MODE.md`（前端相关表述）、`CHANGELOG.md`；给出前后体积对比与双模式验证记录。

并行建议：W1（设计系统）与 W2 的前半（IA 定稿）可以并行；IA 定稿之后才能动组件结构与菜单；
W3 必须在 W2 之后；W4 依赖 W1/W2。

# 七、审查与修复（与优化同期进行的正确姿势）

## 7.1 三段式：体检 → 修复 → 回归复核

优化和修 bug 可以同期做，但**不能混在同一个动作里**，原因：
若一边重构文件结构一边顺手改行为，出问题时无法区分是"重构引入的新缺陷"还是"旧 bug 没修干净"，
而且两类改动混在一个 commit 里，diff 不可审、修复不可验证。所以按下面的节奏走：

1. **体检（W0.5，先于任何改动）**：在所有文件被移动/重写之前抓到问题证据。
   此时仓库还是"原样"，复现步骤与截图才有意义；一旦开始重构，很多现象就再也复现不了了。
2. **修复（随阶段，小步 commit）**：结构重构提交与行为修复提交分开。每个修复必须有
   "复现步骤 → 修复 → 按原步骤验证" 的闭环记录。
3. **回归复核（W7）**：重构后的代码 + 全新上下文做一次独立只读审查，专找重构引入的新问题。

**审查十个维度（W0.5 逐项过，不要只查自己想到的）：**

| # | 维度 | 具体要看什么 |
| --- | --- | --- |
| 1 | 状态与竞态 | 并发轮询、切换任务/板块时的在途请求、提交后立刻刷新、React StrictMode 双调用导致重复请求、定时器与 AbortController 卸载清理、`localStorage` 恢复非法/过期 task_no、轮询上限语义（50 vs 900） |
| 2 | 网络与容错 | 401/403/CSRF 重试路径、超时、离线与恢复、重试风暴、SSE 中断与重连、分片上传续传、错误提示是否可读（不是"任务失败"四个字） |
| 3 | 模式契约 | 线上形态下是否零本地请求（Network 面板核验）、模式是否只有一个来源、`/downloads` 尾斜杠差异、本地登出/登录语义、新加的能力开关是否两端一致 |
| 4 | 国际化 | zh/en key 是否对齐（写脚本对比两份字典的 key 集合）、是否有硬编码中文、数字/日期/复数格式 |
| 5 | 可访问性 | 键盘可达全流程、焦点可见、弹窗焦点陷阱与 Esc、aria 标签、明暗两套对比度、图表类组件的文本替代 |
| 6 | 视觉与布局 | 375 / 768 / 1440 三档宽度、安全区、长文本溢出（Markdown 表格、代码块、超长 prompt）、图片比例、骨架屏与 CLS、暗色漏色 |
| 7 | 性能 | 首屏体积、重复请求、无谓 re-render 热区、动画是否只动 transform/opacity、长列表、图片懒加载 |
| 8 | 前端安全 | Markdown/代码高亮渲染用户内容时的 XSS 面、`dangerouslySetInnerHTML`、外链 `rel="noopener noreferrer"`、CSRF token 处理、控制台/日志是否打印 token 或敏感数据、`localStorage` 是否存了不该存的 |
| 9 | 一致性 | 是否仍有硬编码色值/圆角（应全部走 token）、是否又出现第三份同类实现、同一功能两处文案口径不一致 |
| 10 | 工程门禁 | lint/build 是全绿还是"带病"、CI 是否有质量门禁、`docs/` 里的描述是否与代码一致 |
| 11 | 状态完备性 | 每个异步界面是否有加载 / 空 / 错误 / 部分成功 / 失效五态；是否存在死路（错误态没有下一步）；是否有假实现（按钮无效果、TODO 占位、假数据） |
| 12 | 边界情形 | 超长文本、超大图、上千条历史、慢网、断网恢复、并发任务、额度耗尽、空账号、任务超时——逐条实走，确认被设计过 |

**补充要求：**
- 台账必须能独立复现：写"打开 X → 点 Y → 观察 Z"，不要写"某处状态可能有问题"。
- 定性要诚实：像"本地模式登出返回 `logged_out:false`"这类，很可能是**设计如此**（本地没有登录态），
  真 bug 在前端的无条件跳转；不要把契约当 bug 改掉。
- 本轮明确**不做**的条目要写理由（避免范围失控）；不要为了"顺手"扩大改动面。
- 可自动化的一律自动化：至少给关键链路（本地：提交→轮询→结果；线上：登录→工作台→历史；
  分片上传续传；线上形态零本地请求）写一个可重复执行的冒烟脚本。用 devDependency 的
  Playwright 或宿主浏览器工具均可，**不得**进入运行时依赖。

## 7.2 已知问题候选（先验证再修；每条都要给结论：真 bug / 设计如此 / 已修）

1. 本地模式点「登出」：本地服务返回 `{logged_out:false, local_mode:true}`，但
   `apiService.ts:385-391` 无条件跳 `/workspace?auth=login`，本地没有登录页 → 死路径。
2. 移动端底部导航：侧栏项最多 6 个时 `grid-cols-6`（`Sidebar.tsx:173`），375px 宽下按钮约 55px，
   图标+文案拥挤。需要重新设计移动端（如横向滚动或缩略标签）。
3. 三个工作台 `MAX_POLL_ATTEMPTS` 不一致（50 vs 900，见 §1.4），慢任务在创作台可能被提前放弃。
4. 切 tab 的 `AnimatePresence` 没有 `mode="wait"`（`App.tsx:311`），新旧板块可能短暂叠影。
5. i18n 两份字典漂移（本分支给 workspace 加了 comfy/tts key，console 侧同步滞后）；
   `t: any` 让漏 key 只能运行时发现。
6. 无暗色模式但外壳已声明 `data-theme`；用户切系统暗色时界面完全不变。
7. 无 URL 同步：刷新/前进后退不保留板块与历史选中态（`activeTab` 只在内存）。
   要求：URL 可分享、可前进后退、刷新不丢状态。手写 query/hash 还是引入小路由库由你决定
  （见 §二.3 的条件），但必须在不改 Go 路由的前提下可达（此时只能用 `/workspace`、`/console`
  内部的 query/hash）。
8. 401 处理两端不一致：workspace 跳 `/workspace?auth=login`，console 跳带 `next=/console`
   （`console/src/services/apiService.ts:226-229`）；本地模式两者都不该跳转。
9. 本地 `/downloads/{token}` 与 Go `/downloads/{token}/` 尾斜杠分歧，需验证并统一前端调用方式。
10. `tailwind.config.js` 的 `content` 指向不存在的 `templates/`、`apps/` 目录（Django 残留），
    实际只覆盖到 `static_src`，SSR 样式有遗漏风险；顺带清理死配置。
11. 无 ErrorBoundary：任一板块抛错整页白屏。
12. 图标按钮普遍缺少 `aria-label`；弹窗无焦点圈定与 Esc 关闭（`SettingsModal`、`ComfySettingsModal`、
    `TtsSettingsModal`、`ReleaseNotesModal` 逐个核对）。
13. 生成失败/网络中断时缺少统一错误态与重试入口（三工作台各写各的）。
14. console 表格/分页在窄屏无可用布局（需横向滚动或卡片化）。

# 八、验收清单（必须逐项实测并在报告里给出结果）

命令门禁：
- [ ] `npm run lint` 通过
- [ ] `npm run build` 通过，且 4 个固定产物文件名未变
- [ ] `npm run local` → `npm run local:check` 全部通过

双模式人工验证（各至少一遍，桌面 1440 与移动 375 两档宽度）：
- [ ] 本地模式：文生图 / 图生图 / 小说可视化 / 历史 / Comfy / TTS / 悬浮窗对话 全部可走通；
- [ ] 本地模式：登录弹窗、设置弹窗、发布说明弹窗正常；登出不出现死路径；
- [ ] 线上形态（`DEBUG=1 go run .` 或 `docker build` 后运行）：SSR 页面（登录/注册/账号）与
      工作台/管理台样式一致；Comfy/TTS/悬浮窗入口不存在，Network 面板无 `/api/comfy|tts|chat/*` 请求；
- [ ] 暗色模式三态（light / dark / system）在 SPA 与 SSR 页面均正常，刷新后保持；
- [ ] 断开/恢复网络：上传与轮询给出可理解提示且能恢复；
- [ ] 键盘：Tab 可走完顶栏与侧栏，弹窗焦点圈定，Esc 关闭；
- [ ] 系统开启「减少动态效果」后无动画；
- [ ] 无控制台报错与 React key 警告；React StrictMode 下无重复请求放大。

体积对比（报告里列表）：
- [ ] workspace / console 的 js、css 体积前后对比；线上入口 chunk 是否不再包含 comfy/tts/chat 代码。

审查闭环：
- [ ] `docs/FRONTEND-AUDIT.md` 台账存在，§7.2 候选逐条有运行时结论（含"验证不成立"的条目）；
- [ ] P0/P1 全部闭环（修复 + 复现验证通过），P2 有书面理由；
- [ ] 独立子代理的回归复核结论已记录（或写明未发现的项）；
- [ ] CI 的 lint/build job 在本次改动上通过；
- [ ] 新 IA 的覆盖面自查表逐条打勾（§3.0 能力清单），「旧入口 → 新入口」对照表存在且无遗漏；
- [ ] 抽查 3 处颜色 / 圆角，确认两个应用与 SSR 页面都从同一份 token 取值（无硬编码残留）。

跳转与构建（同样逐项实测）：
- [ ] §3.5 的覆盖清单 8 条全部通过（刷新保留位置、前进后退、旧 `?auth=` 链接、登录回跳、
  401 会话过期、本地模式登出、历史点开三类任务、跨应用互跳 + 移动端返回键）；
- [ ] 站内跳转零整页刷新（DevTools 里 document 请求只出现在报告列明的例外场景）；
- [ ] 全仓搜不到组件里裸写的 `window.location.href`（例外清单之外）；
- [ ] 干净环境 `npm ci && npm run build` 一次通过；4 个契约产物文件名存在；构建输出体积表；
- [ ] SSR 样式与 SPA 样式的 token 来源已同源（抽查 SSR 页面某处颜色改动在两边同时生效）；
- [ ] 死依赖清理后 `npm ci` 与 `docker build` 仍通过（或附书面保留理由）。

产品级走查（§4.1 的标准，逐条给结论与证据）：
- [ ] 五态齐全：抽查工作台 / 历史 / 管理台列表三个界面，加载、空、错误、部分成功、失效都能看到；
- [ ] Slow 3G 限速下：首屏、切板块、提交任务、上传参考图各有可理解的反馈；
- [ ] 断网中途恢复：上传 / 轮询 / SSE 能恢复，或给出明确失败与重试；
- [ ] 超长提示词（5000 字）与超大参考图：不截断、不撑破布局；
- [ ] 上千条历史：滚动流畅、筛选可用；
- [ ] 空账号 / 额度耗尽 / 无模型可选：给出可执行的下一步；
- [ ] 任务进行中刷新：自动接续且状态正确；
- [ ] 未提交内容在切板块 / 误触返回后不丢（或给出保护）；
- [ ] 浏览器标题、favicon、404 与错误页符合设计系统；
- [ ] 生产路径零 console 报错/警告、零 404 请求、零假实现（包括"点了没反应"的按钮）。

# 九、汇报要求

1. 每完成一个 W，在报告里写：改了什么、删了什么、验证命令与结果。
2. 最终报告必须包含：合并后的前端目录树（新增/移动/删除的文件）、设计 token 一览、
   动效清单、`docs/FRONTEND-AUDIT.md` 台账（§7.2 每条候选的运行时结论 + P0/P1 闭环证据 +
   P2 不做的理由）、体积对比、双模式验证记录、独立子代理回归复核的结论、遗留未做项与原因；
   另外必须有：导航/跳转策略说明（路由模型、鉴权跳转规则、整页刷新的例外清单，以及旧链接兼容对照）、
   构建管线说明（脚本表 + 构建期校验输出 + 死依赖处理结论）、
   产品级走查表（§4.1 十条 + §八 边界情形）的全部结论与证据。
3. 提交策略：每个 W 一个 commit，conventional commits（`refactor(frontend): ...` / `fix(frontend): ...`），
   结构重构与行为修复**不混在同一个 commit**；提交前 `npm run lint && npm run build` 必须通过。
   不要推送远端、不要改 `VERSION`。

# 十、反模式（这几种会毁掉成品）

- 不引入第二套设计语言（带样式的整套组件库）；不加运行时外链（字体 / 图标 / 埋点 CDN）；
  不加喧宾夺主的装饰性特效（3D、粒子、渐变网格）。这三条与「用不用某个库」无关，
  是产品感与离线可用性的底线。
- 不改后端 API、数据库、`scripts/localapi/` 的接口契约、Go 路由与资源路径契约。
- 不删本地模式的任何既有能力（Comfy / TTS / 对话 / 小说可视化），也不让线上形态出现本地入口与请求。
- 业务行为与接口语义要保持（任务状态机、权限、数据含义、错误语义不变），但**实现可以完全重写**——
  不要因为「原来就是这么写的」而保留任何实现细节。

---

## 提示词正文结束
