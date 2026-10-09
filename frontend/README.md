# frontend

两个前端应用已合并为一个 npm 包（单一 `package.json` / `package-lock.json` / `node_modules`），
仍产出两份独立构建产物：

| 应用 | 入口 | 产物 |
| --- | --- | --- |
| workspace | `frontend/workspace/index.html` | `static/workspace/index.html`、`static/workspace/assets/workspace.{js,css}` |
| console | `frontend/console/index.html` | `static/console/index.html`、`static/console/assets/console.{js,css}` |

两个应用共用 `frontend/shared/vite.config.base.ts`（Tailwind v4 + React 插件、`@` 别名、
`postcss.plugins = []` 覆盖、`/api` → `http://127.0.0.1:8000` 开发代理），各自的
`vite.config.ts` 只声明应用根目录、产物路径与开发端口。

## 命令（在仓库根目录执行）

```bash
npm install                 # 唯一一次依赖安装

npm run dev:workspace       # workspace 开发服务器 (:3000)
npm run dev:console         # console 开发服务器 (:3001)

npm run build               # 构建全部产物
npm run build:workspace     # 仅构建 workspace 产物
npm run build:console       # 仅构建 console 产物
npm run build:css           # 根目录 Tailwind v3 管线 → static/css/main.css（SSR 页面使用）
npm run lint                # tsc --noEmit（同时检查两个应用与共享配置）
```

`frontend/tsconfig.json` 为两个应用共用的 TypeScript 配置。
