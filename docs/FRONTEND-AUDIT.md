# mirror-studio 前端体检缺陷台账 (W0.5 Audit Ledger)

> 审查基线版本：`bf463fa`（feat/local-tts 及相关本地模式改动）  
> 审查执行：W0.5 运行时与代码审查体检  
> 状态：全部 14 项候选问题与 12 维度深入排查已定性，W1–W6 实施闭环完成（2026-10-10）。

---

## 一、审查概要与优先级统计

| 优先级 | 数量 | 说明 | 修复阶段分布 |
| :--- | :---: | :--- | :--- |
| **P0 (阻塞/死路径/架构核心)** | 4 | 本地登出死路径、无 URL 状态路由、缺少 ErrorBoundary、线上打包混入本地代码 | W2, W3, W6 |
| **P1 (严重体验/功能缺陷)** | 14 | 移动端导航挤压、轮询上限不一致、切页叠影、i18n 漂移与 any、无暗色模式、401 两端不一致、尾斜杠分歧、tailwind.config 路径错误、a11y 缺失、错误态缺失、console 移动端表格溢出等 | W1, W2, W3, W4, W5 |
| **P2 (待办/小瑕疵/记录项)** | 3 | 本地模式 `/downloads` 尾斜杠兼容性（已兼容，规范前端）、ReleaseNotes 外部链接 rel 属性、长列表长远期虚拟化储备 | W2, W6, 记录 |

---

## 二、§7.2 候选清单（1~14）逐项运行时与代码核查结论

### 1. 本地模式点「登出」导致死路径
- **现象**：本地服务 `POST /api/auth/logout` 返回 `{logged_out: false, local_mode: true}`，但前端 `apiService.ts:385-391` 无条件重定向到 `window.location.href = "/workspace?auth=login"`。本地模式没有 SSR 登录页，刷新后弹出要求登录的弹窗，但本地固定账号根本无需登录。
- **复现步骤**：
  1. `npm run local` 启动本地服务并访问 `http://127.0.0.1:8000/workspace`。
  2. 点击右上角用户面板中的「退出登录」。
  3. 观察浏览器地址栏跳转至 `/workspace?auth=login`，出现登录弹窗，产生不可恢复的操作死循环。
- **影响范围**：Local 模式。
- **证据**：`frontend/workspace/src/services/apiService.ts:391`。
- **定性**：**真 bug**。
- **优先级**：**P0**。
- **归属阶段**：**W3 (统一导航与鉴权跳转策略) / W5**。

### 2. 移动端底部导航项挤压（375px 视口）
- **现象**：当本地模式开启时，菜单项多达 6 个（生图、Comfy、TTS、小说、历史、操作），`Sidebar.tsx:173` 使用 `grid-cols-6`，在 375px 宽度下每个按钮宽度不足 52px，中文文字被挤成 3-4 行或严重重叠。
- **复现步骤**：
  1. 打开 Chrome DevTools 并切换至 iPhone SE (375px 宽度)。
  2. 观察底部导航栏，"TTS 语音合成"、"小说可视化" 换行严重，图标与文字交叠。
- **影响范围**：桌面/移动（移动端 375px~640px）。
- **证据**：`frontend/workspace/src/components/Sidebar.tsx:173-194`。
- **定性**：**真 bug / 布局缺陷**。
- **优先级**：**P1**。
- **归属阶段**：**W2 (新 IA 重新归类与收敛) / W4 (响应式导航)**。

### 3. 三个工作台 MAX_POLL_ATTEMPTS 常量不一致（50 vs 900）
- **现象**：`GenerationWorkbench.tsx:84` 中 `MAX_POLL_ATTEMPTS = 50`（按 2.5s 轮询约 125 秒即超时放弃），而 `ComfyWorkbench.tsx:94` 和 `TtsWorkbench.tsx:63` 为 `900`（约 30-45 分钟）。慢速生成任务在普通工作台会被过早判定超时失败。
- **复现步骤**：
  1. 提交耗时较长的生图任务（例如慢速渠道或排队超过 2 分钟）。
  2. 轮询达到 50 次后，前端静默中止轮询并报错提示超时，实际后台任务仍在进行中。
- **影响范围**：Online & Local。
- **证据**：`frontend/workspace/src/components/GenerationWorkbench.tsx:84` vs `ComfyWorkbench.tsx:94`。
- **定性**：**真 bug**。
- **优先级**：**P1**。
- **归属阶段**：**W2 (共享 hooks: useTaskPolling 统一常量与退避重试)**。

### 4. 切 Tab 时 AnimatePresence 缺少 mode="wait"
- **现象**：`App.tsx:311` 中 `<AnimatePresence initial={false}>` 未声明 `mode="wait"`。切换板块时，前一个板块与后一个板块同时存在于 DOM 中并行执行淡出和淡入，导致页面短暂产生两个滚动容器、布局上下跳变和残影。
- **复现步骤**：
  1. 在创作台快速在「创作」与「历史」之间切换。
  2. 观察切换瞬间页面产生瞬时纵向拉伸与跳动。
- **影响范围**：Online & Local。
- **证据**：`frontend/workspace/src/App.tsx:311`。
- **定性**：**真 bug / 动效缺陷**。
- **优先级**：**P1**。
- **归属阶段**：**W4 (视觉与动效规范重构)**。

### 5. i18n 字典漂移且大量使用 t: any
- **现象**：`frontend/console/src/i18n/index.ts` 缺少 ComfyUI、TTS 等词条；`TopToolbar.tsx:29`、`UserPanel.tsx:19`、`Sidebar.tsx:27` 均声明 `t: any`，缺少 TypeScript 类型安全约束，漏 key 无法在构建阶段发现。
- **复现步骤**：
  1. 全局搜索 `t: any`，发现 7 处核心组件存在类型漏洞。
  2. 对比两份 i18n 字典，键集存在结构性不对称。
- **影响范围**：Online & Local。
- **证据**：`frontend/workspace/src/components/Sidebar.tsx:27` 等。
- **定性**：**真 bug / 代码质量缺陷**。
- **优先级**：**P1**。
- **归属阶段**：**W2 (合并唯一类型安全 i18n 层)**。

### 6. 无暗色模式但外壳声明 data-theme
- **现象**：Go SSR 模板 `index.html` 声明 `data-theme="light" data-theme-mode="system"`，但前端全仓无一处 `dark:` 类名或暗色 CSS 变量支持。系统设为暗色模式时，用户界面仍为刺眼白底。
- **复现步骤**：
  1. 系统切换为 Dark 模式。
  2. 刷新页面，前端没有任何暗色适配。
- **影响范围**：Online & Local。
- **证据**：`internal/web/templates/workspace/index.html:2`，前端 0 个 `dark:` 使用。
- **定性**：**功能缺失 / 一致性缺陷**。
- **优先级**：**P1**。
- **归属阶段**：**W1 (Design Tokens tokens.css) / W4**。

### 7. 无 URL 状态同步（刷新与前进后退丢状态）
- **现象**：`activeTab`、`selectedTaskNo`、`modal` 打开状态全部保存在 React 顶层 `useState`，URL 始终为 `/workspace`。刷新页面或点击浏览器后退直接重置回默认生图页，历史任务详情完全丢失。
- **复现步骤**：
  1. 切换到「历史」Tab，点开某个任务大图或详情。
  2. 按 F5 刷新页面。
  3. 页面回到最初的文生图面板，历史选中态全部丢失。
- **影响范围**：Online & Local。
- **证据**：`frontend/workspace/src/App.tsx:84-110`。
- **定性**：**真 bug / 核心体验缺失**。
- **优先级**：**P0**。
- **归属阶段**：**W3 (单一导航层与 URL Query/Hash 状态驱动)**。

### 8. 401 鉴权跳转两端策略不一致
- **现象**：`console/src/services/apiService.ts:227` 硬编码 `window.location.href = "/workspace?auth=login&next=/console"`；而 `workspace/src/services/apiService.ts:229` 抛出 `AuthRequiredError`。本地模式两端均不应整页跳转。
- **复现步骤**：
  1. 本地模式下调用未授权或模拟 401。
  2. 控制台直接跳转到 `/workspace?auth=login`，丢失当前管理台上下文。
- **影响范围**：Online & Local。
- **证据**：`frontend/console/src/services/apiService.ts:227`。
- **定性**：**真 bug**。
- **优先级**：**P1**。
- **归属阶段**：**W3 (集中鉴权跳转与模式感知策略)**。

### 9. 本地 /downloads/{token} 与 Go /downloads/{token}/ 尾斜杠分歧
- **现象**：Go 侧 `internal/media/handlers.go:27` 注册为 `GET /downloads/{token}/`（带尾斜杠），不带斜杠在 Go 1.22 ServeMux 下会触发 301 重定向；Node 本地 API 则通过 `split('/').filter(Boolean)` 两者皆可匹配。
- **复现步骤**：
  1. 审查 Go `internal/media/handlers.go:27` 与 `scripts/localapi/server.mjs:515`。
  2. 前端若调用未带尾斜杠的 URL，线上会多一次重定向网络开销。
- **影响范围**：Online 表现差异。
- **证据**：`internal/media/handlers.go:27` vs `scripts/localapi/store.mjs:273`。
- **定性**：**设计如此但前端需规范兼容**。
- **优先级**：**P1**。
- **归属阶段**：**W2 (统一 downloadURL 工具函数规范带尾斜杠)**。

### 10. tailwind.config.js 的 content 指向不存在的目录
- **现象**：根 `tailwind.config.js` 的 `content` 包含 `'./templates/**/*.html'` 与 `'./apps/**/*.py'`（旧 Django 残留），实际 Go 模板位于 `./internal/web/templates/**/*.html`，导致 Tailwind v3 无法扫描 Go 模板类名。
- **复现步骤**：
  1. 检查根目录，`templates/` 和 `apps/` 目录均不存在。
  2. 若在 `internal/web/templates/` 中添加新 Tailwind 类名，`npm run build:css` 无法提取。
- **影响范围**：Online SSR 页面。
- **证据**：`tailwind.config.js:2-7`。
- **定性**：**真 bug / 死配置**。
- **优先级**：**P1**。
- **归属阶段**：**W1 (管线修正与 content glob 修正)**。

### 11. 全站缺少 ErrorBoundary 导致单点抛错整页白屏
- **现象**：全仓无 `ErrorBoundary` 或 `componentDidCatch`。任何板块内部渲染异常将导致整个 React Fiber 树卸载，整页彻底白屏且无重试入口。
- **复现步骤**：
  1. 模拟一个非预期数据结构（例如本地 store 中被篡改的脏任务数据）。
  2. HistorySpace 渲染抛错，整页白屏，用户无法切换至其他可用板块。
- **影响范围**：Online & Local。
- **证据**：全仓搜索 0 个 ErrorBoundary。
- **定性**：**真 bug / 鲁棒性缺陷**。
- **优先级**：**P0**。
- **归属阶段**：**W2 (Shared ErrorBoundary) / W6 (全局与板块级兜底)**。

### 12. 图标按钮缺 aria-label，弹窗无 Esc 与焦点圈定
- **现象**：`ReleaseNotesModal.tsx`、`SettingsModal.tsx` 等弹窗的关闭按钮缺少 `aria-label`；弹窗容器缺少 `role="dialog"` 与 `aria-modal="true"`，不支持键盘 Esc 键退出与 Tab 键焦点圈定。
- **复现步骤**：
  1. 打开设置弹窗，按下 Esc 键，弹窗无响应。
  2. 按 Tab 键导航，焦点逃逸到弹窗底层的页面元素中。
- **影响范围**：Online & Local。
- **证据**：`frontend/workspace/src/components/ReleaseNotesModal.tsx:59-65`。
- **定性**：**真 bug / a11y 缺陷**。
- **优先级**：**P1**。
- **归属阶段**：**W2 (封装共享 Modal/Dialog 组件) / W4**。

### 13. 生成失败与网络中断缺少统一错误态与重试入口
- **现象**：`GenerationWorkbench`、`ComfyWorkbench`、`TtsWorkbench` 各自独立编写了轮询失败逻辑，错误时仅显示一条浮动 Toast，结果区退回初始空态或无限 Loading，没有带具体原因与「一键重试」的操作卡片。
- **复现步骤**：
  1. 断网或服务端返回 500。
  2. 任务显示失败，但主面板上没有清晰的错误重试卡片，用户只能重新输入一遍参数。
- **影响范围**：Online & Local。
- **证据**：三套 Workbench 的轮询 catch 块各不相同。
- **定性**：**真 bug / 状态不完备**。
- **优先级**：**P1**。
- **归属阶段**：**W2 (useTaskPolling) / W4 (统一五态组件)**。

### 14. console 表格在移动端/窄屏视口无横向滚动或适配
- **现象**：`console/src/App.tsx:611` 的表格容器包裹了 `overflow-hidden`，在 375px~640px 视口下表格列被硬性裁剪，无法水平滑动查看完整数据。
- **复现步骤**：
  1. 移动端 375px 宽度打开管理台 `/console`。
  2. 切换到用户管理或任务管理表格，右侧字段（注册时间、操作按钮）被裁切且不可滚动。
- **影响范围**：Console 移动端。
- **证据**：`frontend/console/src/App.tsx:611`。
- **定性**：**真 bug / 响应式缺陷**。
- **优先级**：**P1**。
- **归属阶段**：**W4 (Console 架构与表格卡片化重写)**。

---

## 三、§7.1 十二个维度新增排查发现

| 维度 | 发现具体问题 | 定性 | 优先级 | 修复阶段 |
| :--- | :--- | :---: | :---: | :---: |
| **1. 状态与竞态** | `useEffect` 轮询定时器在快速切 Tab 时虽有 ref 取消，但在途中 fetch 未绑定 `AbortController`，响应返回时仍可能触发未挂载组件状态更新警告。 | 真 bug | P1 | W2 |
| **2. 模式契约与打包** | 线上 Go 产物中静态 import 了 `ComfyWorkbench`、`TtsWorkbench`、`ChatWidget`，导致线上包携带本地无用代码，体积膨胀至 674KB。 | 架构缺陷 | P0 | W3 |
| **3. 字体外链** | `index.css` 和 SSR 模板引入 Google Fonts 外部网络链接，内网/离线/无外网环境首屏样式阻塞挂起。 | 体验/安全 | P1 | W1 |
| **4. 安全与 XSS** | Markdown 渲染中外部链接缺少 `rel="noopener noreferrer"` 和 `target="_blank"` 安全属性。 | 安全隐患 | P2 | W4 |
| **5. 假实现与 TODO** | 管理台概览卡片中部分快捷跳转按钮点击无反馈；部分图表为空占位容器。 | 产品缺陷 | P1 | W4 |
| **6. 页面五态缺失** | 历史记录页网络错误或加载中时整块闪烁，无骨架屏（CLS 明显），空数据无引导动作。 | 状态缺陷 | P1 | W2/W4 |
| **7. 草稿保存连续性** | 刷新页面或切换板块后，用户未提交的提示词与配置丢失，无 LocalStorage 自动草稿保护。 | 体验缺陷 | P1 | W3/W4 |

---

## 四、修复与实施路线归属映射

- **W1 (设计系统)**：解决 #6 (暗色模式)、#10 (tailwind.config content 修正)、外链字体清理、Tokens 统一。
- **W2 (IA与共享层)**：解决 #3 (轮询上限与逻辑统一)、#5 (i18n 合并与类型化)、#9 (尾斜杠规范)、#11 (ErrorBoundary)、#12 (a11y Modal 原语)、#13 (五态错误卡片)、新增状态竞态治理。
- **W3 (模式契约与导航)**：解决 #1 (本地登出死路径)、#7 (URL 状态与单一导航)、#8 (401 鉴权一致性)、线上代码拆包与懒加载 (消除 674KB 膨胀)。
- **W4 (视觉与动效重写)**：解决 #2 (移动端导航)、#4 (AnimatePresence wait)、#14 (console 响应式表格)、全站高级感与微动效落地。
- **W5 (台账复验与收尾)**：对本台账所有 P0/P1 条目按原复现步骤逐一进行回归验证并记录通过证据。
- **W6 (构建管线与门禁)**：死依赖清理、CI lint/build 门禁、构建产物体积监控。

---

## 五、实施闭环结论（W7 回填，2026-10-10）

### §7.2 候选 1–14 逐条闭环状态

| # | 候选问题 | 最终结论 | 证据 |
| --- | --- | --- | --- |
| 1 | 本地登出死路径 | **已修** | UserPanel 改走 shared `logout`（模式感知，本地不跳转，15413bb）；线上从 console 登出带 `next=%2Fconsole` 回跳。浏览器实测本地登出停回 `/workspace` 原地。 |
| 2 | 移动端底部导航 375px 挤压 | **已修** | Sidebar 底部导航从 `grid-cols-6` 改为横滑胶囊布局（c6a5267），375px 实测无换行无重叠。 |
| 3 | MAX_POLL_ATTEMPTS 50 vs 900 | **已修** | 三工作台统一 900 次 × 2.5s ≈ 37.5 分钟（900 是正确语义：云端排队+慢渠道），Generation 的 MAX_POLL_LIFETIME_MS 8min→35min 与 TTS 对齐（15413bb）。shared `useTaskPolling` 同源常量。 |
| 4 | AnimatePresence 缺 mode="wait" | **已修** | workspace App.tsx 已补 `mode="wait"`（15413bb）；console 拆包时即带。 |
| 5 | i18n 漂移 + t: any | **已修** | 两份旧字典删除（console i18n 目录已删，workspace i18n 仅存过渡期本地文案）；shared/i18n 单一字典 + 类型安全 `TranslationDictionary`；console 全部文案走 `t.console.*`；LanguageProvider 双应用接入，语言持久化 `mirror.language`。workspace 大组件（workbench 系）文案仍在过渡字典，随组件重写逐步收敛到 shared。 |
| 6 | 无暗色模式 | **已修** | tokens.css 明暗两套 + useTheme 三态（light/dark/system）持久化 `mirror.theme`；console 顶栏切换按钮实测暗色正确（无白底漏色）；SSR 外壳 data-theme 对接。 |
| 7 | 无 URL 状态同步 | **已修** | workspace `?tab=` + `?task=&task_kind=` 结构化入 URL，popstate/刷新/首屏恢复全部生效（cf32558，浏览器实测）；console `?tab=` 自拆包起即 URL 驱动。旧 `?auth=login|register`、`?next=`、`comfy:`/`tts:`/`novelviz:` 前缀仍可解析。 |
| 8 | 401 两端策略不一致 | **已修** | 401 只在 shared/client.ts 一处处理：线上触发 `setOnAuthRequired` 监听（App 层弹登录框），本地只抛错不跳转；console 旧 302 跳转代码随 apiService 删除（15413bb）。 |
| 9 | /downloads 尾斜杠分歧 | **已兼容** | shared `formatDownloadUrl` 统一带尾斜杠（Go ServeMux 与 Node 本地 API 都直接命中，无 301）。 |
| 10 | tailwind.config.js content 指向不存在目录 | **已修** | W1 已改为 `./internal/web/templates/**/*.html` 等真实路径。 |
| 11 | 无 ErrorBoundary | **已修** | shared ErrorBoundary（类组件 + i18n fallback），console App 每个视图包裹；workspace 侧栏/主区在 W3 已有局部包裹。 |
| 12 | 图标按钮缺 aria-label / 弹窗无焦点圈定 | **已修** | shared IconButton 强制 aria-label；Modal 内建焦点陷阱+Esc；SettingsModal/AuthModal/Comfy/Tts 设置弹窗接入 `useDialogA11y`（15413bb，浏览器实测焦点圈定/Esc/滚动锁生效）。 |
| 13 | 生成失败缺统一错误态 | **部分完成** | shared ErrorState/EmptyState 五态组件就绪并被 console 全量使用；三个 workbench 的失败路径仍以 Toast 为主，轮询终止时有"到历史记录手动刷新"提示。剩余收敛依赖 workbench 组件重写（W4 遗留，见"明确不做"）。 |
| 14 | console 表格窄屏无布局 | **已修** | 新表格全部 `overflow-x-auto` + `min-w-[...]`，375px 可横滑查看全列（浏览器实测）。 |

### 明确不做 / 遗留（附理由）

- **#13 收尾（workbench 内错误卡片）**：三个 workbench 合计约 5000 行，是 W4 建议合并的"生成页"重构主体；在未重写组件结构前逐个打补丁会让迁移更难。当前失败路径有 Toast + 轮询护栏 + 历史可恢复，无死路。留待下一轮把 Comfy/TTS 引擎降级为生成页引擎选项时一并处理。
- **Markdown 外链 rel（P2）**：react-markdown 组件层未找到用户可控的 `<a target=_blank>` 渲染路径（聊天与小说日志均未开 link 渲染），风险不成立，保持记录。
- **长列表虚拟化**：历史记录分页加载，千条场景无卡顿证据，不做。
- **workspace 旧 i18n 过渡字典**：workspace/src/i18n 尚存（App/TopToolbar/UserPanel 已迁 shared，workbench 系未迁）；为保证本轮"能力不丢"的边界，组件级迁移留给 workbench 重写轮。

