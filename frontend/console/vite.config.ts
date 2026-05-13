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
      // 固定 console 使用自己的 Tailwind/Vite 处理，避免读取根目录静态页的 PostCSS 配置。
      postcss: {
        plugins: [],
      },
    },
    build: {
      // Output to Django static directory for production deployment
      outDir: '../../static/console',
      emptyOutDir: true,
      rollupOptions: {
        output: {
          entryFileNames: 'assets/console.js',
          chunkFileNames: 'assets/[name]-[hash].js',
          assetFileNames: 'assets/console.[ext]',
        },
      },
    },
    server: {
      port: 3001,
      hmr: process.env.DISABLE_HMR !== 'true',
    },
  };
});
