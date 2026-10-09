// workspace 与 console 两个前端应用共用的 Vite 配置工厂，避免两份配置重复维护。
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import {createRequire} from 'module';
import path from 'path';
import {defineConfig} from 'vite';

// 合并为单个 npm 包后，根 node_modules 顶层的 tailwindcss 是 v3（根目录 build:css 的 CLI 需要），
// 两个应用使用的 Tailwind v4 由 @tailwindcss/vite / @tailwindcss/node 依赖带入。
// 这里沿该依赖边解析出 v4 的 CSS 入口，保证 src/index.css 中 `@import "tailwindcss"` 的解析结果与合并前一致。
function resolveTailwindCssEntry(): string {
  const require = createRequire(import.meta.url);
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

const tailwindCssEntry = resolveTailwindCssEntry();

export interface SpaConfigOptions {
  /** 应用根目录（index.html 所在目录），由调用方传入 __dirname。 */
  root: string;
  /** 构建输出目录，相对 root 解析。 */
  outDir: string;
  /** 产物基线文件名，例如 workspace / console。 */
  assetName: string;
  /** 开发服务器端口。 */
  port: number;
}

export function createSpaConfig({root, outDir, assetName, port}: SpaConfigOptions) {
  return defineConfig(() => {
    return {
      root,
      plugins: [react(), tailwindcss()],
      resolve: {
        alias: {
          '@': path.resolve(root, '.'),
          // 仅重定向裸说明符 `tailwindcss`（CSS @import 使用），不匹配 `tailwindcss/...` 子路径。
          tailwindcss: tailwindCssEntry,
        },
      },
      css: {
        // 固定各应用使用自己的 Tailwind v4 Vite 插件，避免向上读取根目录 Tailwind v3 的 postcss.config.js。
        postcss: {
          plugins: [],
        },
      },
      build: {
        outDir,
        emptyOutDir: true,
        rollupOptions: {
          output: {
            entryFileNames: `assets/${assetName}.js`,
            chunkFileNames: 'assets/[name]-[hash].js',
            assetFileNames: `assets/${assetName}.[ext]`,
          },
        },
      },
      server: {
        port,
        // HMR is disabled in AI Studio via DISABLE_HMR env var.
        // Do not modify — file watching is disabled to prevent flickering during agent edits.
        hmr: process.env.DISABLE_HMR !== 'true',
        proxy: {
          '/api': {
            target: 'http://127.0.0.1:8000',
            changeOrigin: true,
          },
        },
      },
    };
  });
}
