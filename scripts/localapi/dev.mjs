#!/usr/bin/env node
// 本地开发者模式：单进程、单端口同时提供本地 API、工作台与管理台。
//
//   npm run local        # 一个端口（默认 8000）：/ 工作台，/admin 管理台
//   npm run local:api    # 只跑本地 API（供 npm run dev:workspace / dev:console 代理）
//   npm run local:check  # 接口自检（默认自己起隔离实例，不写 local-data）
//
// 前端页面由 Vite 以 middleware 模式挂在这个进程的 HTTP 服务上：
// HMR 的 WebSocket 也走同一个端口，所以对外只需要暴露一个地址。
import http from 'node:http';

import * as chatApi from './chat.mjs';
import {DATA_DIR, HOST, IMAGE_SIZE, PORT, PROVIDER, STORE_FILE, providerConfigured, ttsConfigured, TTS} from './config.mjs';
import * as comfyApi from './comfy.mjs';
import {handleRequest, isApiPath, reportListenError} from './server.mjs';
import {createLocalWebServer} from './web.mjs';

let web = null;

const server = http.createServer((req, res) => {
  const pathname = new URL(req.url, `http://${req.headers.host || 'localhost'}`).pathname;
  if (isApiPath(pathname)) {
    handleRequest(req, res).catch((error) => {
      console.error(`[local-api] ${req.method} ${pathname} 处理失败：`, error);
      if (!res.headersSent) {
        res.writeHead(500, {'Content-Type': 'application/json; charset=utf-8'});
        res.end(JSON.stringify({code: 500, message: '本地服务内部错误', data: null}));
      } else {
        res.end();
      }
    });
    return;
  }
  // 其余路径（含热更新 WebSocket 之外的普通请求）交给 Vite 处理。
  web.middlewares(req, res, () => {
    if (res.writableEnded) return;
    res.writeHead(404, {'Content-Type': 'text/plain; charset=utf-8'});
    res.end(`本地服务没有这个路径：${req.method} ${req.url}`);
  });
});

web = await createLocalWebServer(server);

// 启动时探一次 ComfyUI：板块只有本地模式才存在，这里给个一眼可见的状态。
const comfy = await comfyApi.comfyStatus();
const chat = chatApi.chatConfigView();

server.on('error', reportListenError);
server.listen(PORT, HOST, () => {
  const origin = `http://${HOST}:${PORT}`;
  console.log('');
  console.log('  绘镜 · 本地开发者模式（单端口）');
  console.log('  ────────────────────────────────────────────────');
  console.log(`  工作台        ${origin}/          （改前端代码即时热更新）`);
  console.log(`  管理台        ${origin}/admin`);
  console.log(`  本地 API      ${origin}/api/…`);
  console.log(`  数据目录      ${DATA_DIR}`);
  console.log(`  生图渠道      ${providerConfigured ? `真实渠道 ${PROVIDER.modelName}` : '本地占位图（零配置可用）'}`);
  console.log(`  对话渠道      ${chat.configured ? `真实渠道 ${chat.model}（${chat.base_url} · 思考等级 ${chat.reasoning_effort}）` : '本地模拟回复（工作台悬浮窗 → 设置里配置中转站）'}`);
  console.log(`  Comfy 生图    ${comfy.running ? `ComfyUI 运行中（${comfy.base_url}）` : `ComfyUI 未运行（${comfy.base_url}；在工作台「Comfy 生图」板块可一键启动）`}`);
  console.log(`  TTS 语音      ${ttsConfigured ? `CLI 就绪（${TTS.cli}）` : `未找到 TTS CLI（${TTS.cli}；板块会显示引导）`}`);
  console.log(`  图片尺寸      ${IMAGE_SIZE}`);
  console.log('  ────────────────────────────────────────────────');
  console.log('  无数据库、无登录、无注册；删除 local-data/ 即可重置。');
  console.log(`  API 状态页    ${origin}/__local   （存储文件 ${STORE_FILE}）`);
  console.log('');
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    server.close();
    web.close().finally(() => process.exit(0));
  });
}
