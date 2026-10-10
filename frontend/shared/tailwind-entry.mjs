// Tailwind v4 的 CSS 入口解析：workspace 与 console 两个应用、以及本地模式的
// 单端口开发服务器都用这一份，避免同一段解析逻辑散落在三处。
//
// 背景：合并为单个 npm 包后，根 node_modules 顶层的 tailwindcss 是 v3
// （根目录 build:css 的 CLI 需要），两个前端应用实际用的是 v4，由
// @tailwindcss/vite / @tailwindcss/node 依赖带入。这里沿该依赖边解析出 v4 的
// index.css，保证 CSS 中 `@import "tailwindcss"` 的解析结果与合并前一致。
import {createRequire} from 'node:module';
import path from 'node:path';

const require = createRequire(import.meta.url);

export function resolveTailwindCssEntry() {
  for (const plugin of ['@tailwindcss/vite', '@tailwindcss/node']) {
    try {
      return require.resolve('tailwindcss/index.css', {
        paths: [path.dirname(require.resolve(plugin))],
      });
    } catch {
      // 尝试下一个插件位置
    }
  }
  throw new Error('无法从 @tailwindcss/vite 或 @tailwindcss/node 解析 Tailwind v4 的 index.css');
}
