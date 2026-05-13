import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, '.'),
      },
    },
    css: {
      // 固定 workspace 使用自己的 Tailwind v4 Vite 插件，避免向上读取根目录 Tailwind v3 的 postcss.config.js。
      postcss: {
        plugins: [],
      },
    },
    build: {
      // Output to Django static directory for production deployment
      outDir: '../../static/workspace',
      emptyOutDir: true,
      rollupOptions: {
        output: {
          entryFileNames: 'assets/workspace.js',
          chunkFileNames: 'assets/[name]-[hash].js',
          assetFileNames: 'assets/workspace.[ext]',
        },
      },
    },
    server: {
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
