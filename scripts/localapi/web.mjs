// 本地模式的 Web 层：一个 Vite 开发服务器同时服务工作台与管理台，
// 通过路径区分（/ 与 /workspace 是工作台，/admin 与 /console 是管理台），
// 并与本地 API 共用同一个端口。
//
// 为什么是一个 Vite 实例而不是两个：两个实例要共享同一个端口就必须各自
// 处理 HMR 的 WebSocket 路径，配置脆弱；单实例是 Vite 的标准用法，热更新、
// 依赖预构建、模块图都只有一份，两个应用靠入口路径（/workspace/、/console/）
// 互不干扰，也和生产产物路径一致。
import path from 'node:path';

import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import {createServer} from 'vite';

import {ROOT_DIR} from './config.mjs';
import {applyAppVersionEnv} from '../../frontend/shared/app-version.mjs';
import {resolveTailwindCssEntry} from '../../frontend/shared/tailwind-entry.mjs';

// 版本号必须在这里就写进 process.env：Vite 在 resolveConfig 阶段读取 VITE_* 环境变量，
// 之后 import.meta.env.VITE_APP_VERSION 才能在 dev 下拿到值（与生产构建同一个来源）。
applyAppVersionEnv();

const FRONTEND_DIR = path.join(ROOT_DIR, 'frontend');

// 入口路径 → 该应用真正的 index.html。只精确匹配入口，模块请求
// （/workspace/src/...、/console/src/...、/@vite/client…）原样放行。
const ENTRY_REWRITES = new Map([
  ['/', 'workspace'],
  ['/index.html', 'workspace'],
  ['/workspace', 'workspace'],
  ['/workspace/', 'workspace'],
  ['/admin', 'console'],
  ['/admin/', 'console'],
  ['/admin/index.html', 'console'],
  ['/console', 'console'],
  ['/console/', 'console'],
]);

function localWebPlugin() {
  return {
    name: 'mirror-local-web',
    // index.html 里的 /src/main.tsx 是相对应用根写的（两个应用各自 dev 时用得上），
    // 共享一个 root 时要改成带应用前缀的绝对路径，否则会被解析到 frontend/src。
    transformIndexHtml: {
      order: 'pre',
      handler(html, context) {
        const file = String(context.filename || context.path || '').replace(/\\/g, '/');
        const app = file.includes('/console/') ? 'console' : file.includes('/workspace/') ? 'workspace' : null;
        if (!app) return html;
        return html.replace(/(src|href)="\/src\//g, `$1="/${app}/src/`);
      },
    },
    configureServer(server) {
      // 这个中间件在 configureServer 里直接注册，先于 Vite 内部中间件执行，
      // 因此改写 req.url 后，Vite 会把对应应用的 index.html 当成普通 HTML 处理。
      server.middlewares.use((req, res, next) => {
        const raw = req.url || '/';
        const separator = raw.indexOf('?');
        const pathname = separator >= 0 ? raw.slice(0, separator) : raw;
        const query = separator >= 0 ? raw.slice(separator) : '';

        const entry = ENTRY_REWRITES.get(pathname);
        if (entry) {
          req.url = `/${entry}/index.html${query}`;
          return next();
        }

        // 与 Vite 的 SPA 回退一致：浏览器导航（Accept: text/html）到没有扩展名的
        // 未知路径时给出工作台外壳，方便手敲 /workspace 之类的地址；其余仍按 404 处理。
        const acceptsHTML = String(req.headers.accept || '').includes('text/html');
        const looksLikeFile = path.extname(pathname) !== '';
        if (acceptsHTML && !looksLikeFile && pathname !== '/__local') {
          req.url = `/workspace/index.html${query}`;
        }
        return next();
      });
    },
  };
}

/** 创建与本地 API 共用端口的 Vite 开发服务器（middleware 模式）。 */
export async function createLocalWebServer(httpServer) {
  return createServer({
    root: FRONTEND_DIR,
    // mpa：由 Vite 负责把请求到的 .html 文件转成页面（含入口改写与热更新注入），
    // 但不像 spa 那样把未知路径一律回退成 index.html——回退由下面的中间件按 Accept 决定。
    appType: 'mpa',
    plugins: [react(), tailwindcss(), localWebPlugin()],
    resolve: {
      alias: {
        // 与 frontend/shared/vite.config.base.ts 的 createSpaConfig 保持同源：
        // 两端 dev 与 build 用同一套 @shared 别名，避免“构建能过、本地白屏”。
        '@shared': path.join(FRONTEND_DIR, 'shared'),
        // 仅重定向裸说明符 `tailwindcss`（CSS @import 使用），不匹配子路径。
        tailwindcss: resolveTailwindCssEntry(),
      },
    },
    css: {
      // 固定使用 Tailwind v4 的 Vite 插件，避免向上读取根目录 Tailwind v3 的 postcss.config.js。
      postcss: {plugins: []},
    },
    server: {
      middlewareMode: true,
      // HMR 复用本地 API 的 HTTP 服务，因此只有一个端口对外。
      hmr: {server: httpServer},
    },
  });
}
