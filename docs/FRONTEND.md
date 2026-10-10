# FRONTEND.md — 前端结构与构建

0.0.1 把原来的两份前端包（`frontend/workspace` + `frontend/console` 各自带
package.json / lockfile / node_modules）合并成了**一个 npm 包**：根
`package.json` 管全部依赖，两套应用只保留各自的入口与 Vite 配置，
共享部分在 `frontend/shared/vite.config.base.ts`。

```
package.json                  # 唯一的依赖清单与构建脚本
frontend/
  shared/vite.config.base.ts  # SPA 配置工厂（react + tailwind v4 + 产物命名）
  workspace/                  # 用户侧 SPA：index.html + src + vite.config.ts
  console/                    # 控制台 SPA：同上，产物路径互不重叠
  tsconfig.json               # 两应用共用的类型检查工程（npm run lint）
static_src/css/tailwind.css   # SSR 页面样式的 Tailwind v3 入口
static/                       # 构建产物目录（gitignore，Go 以 go:embed 打包）
  css/main.css                # ← npm run build:css
  workspace/                  # ← npm run build:workspace（vite outDir）
  console/                    # ← npm run build:console
  favicon.svg, images/        # 仓库内静态资源（随 embed 进入二进制）
```

## 构建与产物

```bash
npm run build     # = build:css → build:workspace → build:console → build:ssr
```

- `build:css`：Tailwind v3 CLI 把 `static_src/css/tailwind.css` 压缩成
  `static/css/main.css`，供 Go 渲染的 SSR 页面（`/login`、`/register` 等）引用。
- `build:workspace` / `build:console`：Vite v6 + Tailwind v4（经
  `@tailwindcss/vite` 插件），产物文件名固定为 `assets/workspace.*` /
  `assets/console.*`，保持与既有 Go 模板相同的引用路径契约。
- `build:ssr`：说明性空操作。SSR 页面不是构建产物——模板内嵌在 Go 二进制里，
  其 CSS 依赖 `build:css`。

> 0.0.2 重构解除旧有「SPA 运行时逐字节一致」约束，正式落地统一设计系统与双模式一致性。

## 设计系统 (Design System)

全站确立**「白底静奢极简」（Quiet Minimal Light）**设计语言，两套 SPA 与 SSR 页面统一从单一 Token 来源取值：

1. **唯一 Token 来源**：`frontend/shared/design/tokens.css`。纯 CSS 变量定义中性色阶（`--color-bg`, `--color-surface`, `--color-ink`, `--color-border`）、唯一强调色（`--color-accent: #6d5dfc`）、语义色、标准圆角阶（`xs: 6px`, `sm: 8px`, `md: 12px`, `lg: 16px`, `xl: 22px`, `2xl: 28px`）与三级阴影（`raised`, `floating`, `overlay`）。
2. **两套管线同源消费**：
   - **SPA (Tailwind v4)**：各应用 `index.css` 通过 `@import "../../shared/design/tokens.css"` 引入并经 `@theme` 映射语义类名；通过 `@source "../../shared"` 保证共享层样式提取。
   - **SSR (Tailwind v3)**：`tailwind.config.js` 与 `static_src/css/tailwind.css` 只引用同名 CSS 变量，模板内联覆盖样式已全部收敛至 `@layer components`。
   - 改动一次 CSS 变量，SPA 与 Go SSR 页面即时同步生效。
3. **字体规范**：消灭外部 Google Fonts 网络阻塞依赖。统一采用零外链的系统字体栈：
   - 默认正文/标题：`-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, "PingFang SC", "Microsoft YaHei", "Noto Sans SC", sans-serif`
   - 等宽代码：`ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, Monaco, Consolas, monospace`
4. **暗色模式 (Dark Mode)**：
   - 支持 `[data-theme="dark"]`、`prefers-color-scheme` 系统跟随，持久化至 `localStorage("mirror.theme")`（`light` / `dark` / `system` 三态）。
   - SSR 模板外壳通过 `data-theme` 属性无缝衔接。

## 信息架构重设计 (Information Architecture)

### 1. 创作台 (Workspace)
- **一级入口**：按用户任务心智划分两大核心板块（层级≤2）：
  - **「创作」(Create / Studio)**：所有生成能力聚合入口。
    - **图像创作 (Image)**：包含云端生图（文生图/图生图/参考图上传）与 本地 ComfyUI 生图（Local 专属引擎选项，支持 4 模型与显存监控）。
    - **语音合成 (Audio)**（Local 专属）：四种模式（音色设计、声音克隆、音色复用、预设音色）+ 音色库抽屉 + 引擎实例与 GPU 管理。
    - **小说绘卷 (Story)**：EPUB 解析、场景提取、SSE 实时日志、场景图分镜与画卷。
    - **AI 对话助手 (Assistant)**（Local 专属悬浮窗）：提示词生成与一键「填回描述」。
  - **「画廊与历史」(History)**：跨模态全任务管理中心。
    - 统一多模态筛选（全部/图像/语音/小说），结构化回跳对应编辑器，彻底废除 `comfy:`/`tts:`/`novelviz:` 字符串前缀。
- **为什么这样分**：聚合生产动作（输入）与资产沉淀（产出），不再按技术引擎割裂导航，新手认知成本直降。

### 2. 管理台 (Console)
- 按管理对象收敛为 5 个核心领域：
  - **「概览」(Overview)**：核心指标看板与动态。
  - **「用户」(Users)**：用户检索、配额调整抽屉、状态管理。
  - **「任务」(Tasks)**：跨引擎任务流排查与生成审计。
  - **「渠道」(Channels)**：图像与对话渠道健康监控。
  - **「设置」(Settings)**：站点配置与本地 Comfy/TTS 日志。

### 3. 旧入口 → 新入口对照表
| 旧入口 | 新入口 | 对应 URL / 状态 |
| :--- | :--- | :--- |
| `activeTab === 'create'` (生图) | 创作台 → 图像创作 (云端引擎) | `/workspace?tab=create&sub=image&engine=cloud` |
| `activeTab === 'comfy'` (ComfyUI) | 创作台 → 图像创作 (ComfyUI 引擎) | `/workspace?tab=create&sub=image&engine=comfy` |
| `activeTab === 'tts'` (TTS 合成) | 创作台 → 语音合成 | `/workspace?tab=create&sub=audio` |
| `activeTab === 'novelviz'` (小说绘卷) | 创作台 → 小说绘卷 | `/workspace?tab=create&sub=story` |
| `activeTab === 'history'` (历史记录) | 画廊与历史 | `/workspace?tab=history` |
| 悬浮窗对话 | 创作台右下角 AI 助手气泡 | 随创作台常驻，可折叠 |
| Console Tab 各项 | Console 模块化视图 | `/console?tab=overview\|users\|tasks\|channels\|settings` |


## 运行时接线

- **唯一运行时**：Go 从 `go:embed all:static` 直接服务 `/static/**`；SPA 路由由
  Go 端按 1.x 的 URL 规则返回入口页。镜像内没有 node，也不需要。
  服务始终只监听一个端口（`APP_PORT`，默认 8000），页面里不存在指向其它端口的
  分支——容器只暴露这一个端口。
- **前端热更新**：单端口的 `npm run local`（Vite middleware，见
  `docs/LOCAL-MODE.md`）；或按需单独起 `npm run dev:workspace` /
  `npm run dev:console`（各自端口、自带 HMR，把 `/api` 代理回后端的 8000）。
  它们只服务自己的页面，不再被 Go 注入到 `/workspace`、`/console` 页面里。

## 已知保留项（0.0.1 不动）

- 根 `package.json` 中 `@google/genai`、`express`、`dotenv`、`tsx`、
  `@types/express` 已无源码引用，保留是为了依赖解析与既定 lockfile 的
  逐字节稳定，0.0.2 再清理。
- `frontend/console/src/components/` 中与 workspace 同名的历史副本组件已随
  S1–S4 清理；若再发现不可达副本，删除门禁是截图零差异。
