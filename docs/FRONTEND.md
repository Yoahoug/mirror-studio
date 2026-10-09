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
  `assets/console.*`，保持与 1.x 相同的引用路径。
- `build:ssr`：说明性空操作。SSR 页面不是构建产物——模板内嵌在 Go 二进制里，
  这里仅用于提示其 CSS 依赖 `build:css`。

SPA 的运行时渲染输出不得改动（重写的冻结约束），组件、文案与资源引用路径
与 1.x 逐字节一致；合并包只动了构建管线，不动 UI 源码语义。

## 运行时接线

- **生产**：Go 从 `go:embed all:static` 直接服务 `/static/**`；SPA 路由由
  Go 端按 1.x 的 URL 规则返回入口页。镜像内没有 node，也不需要。
- **开发（DEBUG=1）**：`WORKSPACE_VITE_DEV_URL` / `CONSOLE_VITE_DEV_URL`
  （默认 `http://127.0.0.1:3000|3001`）被注入页面，指向 Vite 开发服务器；
  Vite 把 `/api` 代理回 Go 的 8000 端口。

## 已知保留项（0.0.1 不动）

- 根 `package.json` 中 `@google/genai`、`express`、`dotenv`、`tsx`、
  `@types/express` 已无源码引用，保留是为了依赖解析与既定 lockfile 的
  逐字节稳定，0.0.2 再清理。
- `frontend/console/src/components/` 中与 workspace 同名的历史副本组件已随
  S1–S4 清理；若再发现不可达副本，删除门禁是截图零差异。
