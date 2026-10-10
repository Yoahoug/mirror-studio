// workspace 与 console 两个前端应用共用的 Vite 配置工厂，避免两份配置重复维护。
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';

// 应用版本号（仓库根 VERSION）与 Tailwind v4 的 CSS 入口解析：
// 本地模式的单端口 dev server 也用同一份实现，避免两处定义漂移。
import {applyAppVersionEnv} from './app-version.mjs';
import {resolveTailwindCssEntry} from './tailwind-entry.mjs';

// 必须在 defineConfig 之前执行：Vite 在 resolveConfig 阶段才读取 VITE_* 环境变量。
applyAppVersionEnv();

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
  // 开发代理目标：默认 127.0.0.1:8000。两种开发方式共用这一个地址：
  // - 本地开发者模式（npm run local）：8000 上是 scripts/localapi 的零依赖本地服务；
  // - Go 后端开发（DEBUG=1 go run .）：8000 上是 Go 服务本身。
  // 前端只认相对路径，两种后端下代码都不需要改动。
  const apiTarget = process.env.LOCAL_API_TARGET || 'http://127.0.0.1:8000';
  const sharedDir = path.resolve(root, '../shared');
  return defineConfig(({command}) => {
    return {
      root,
      base: command === 'build' ? `/static/${assetName}/` : '/',
      plugins: [react(), tailwindcss()],
      resolve: {
        alias: {
          '@': path.resolve(root, '.'),
          '@shared': sharedDir,
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
        // 端口被占用时直接失败，而不是悄悄换到 3001/3002：
        // 本地模式的地址是固定的，静默漂移只会让代理和文档对不上。
        strictPort: true,
        // HMR is disabled in AI Studio via DISABLE_HMR env var.
        // Do not modify — file watching is disabled to prevent flickering during agent edits.
        hmr: process.env.DISABLE_HMR !== 'true',
        proxy: {
          '/api': {target: apiTarget, changeOrigin: true},
          '/console/api': {target: apiTarget, changeOrigin: true},
          '/downloads': {target: apiTarget, changeOrigin: true},
        },
      },
    };
  });
}
