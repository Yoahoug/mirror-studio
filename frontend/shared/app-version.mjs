// 应用版本号：唯一来源是仓库根 VERSION（发布脚本 scripts/release.py 是它的唯一写入者）。
// 两个前端的 Vite 配置（frontend/shared/vite.config.base.ts）与本地模式的单端口
// dev server（scripts/localapi/web.mjs）都用这里的值来定义 __APP_VERSION__，
// 让页脚在构建产物与本地热更新下显示同一个版本。
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const VERSION_FILE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..', 'VERSION');

export function readAppVersion() {
  try {
    const value = fs.readFileSync(VERSION_FILE, 'utf8').trim();
    if (value) return value;
  } catch {
    // 读取失败时给出可辨识的兜底值（构建机器上 VERSION 一定存在）
  }
  return '0.0.0';
}

export const APP_VERSION = readAppVersion();

/**
 * 把版本号作为真实的 VITE_ 环境变量放进 process.env：
 * Vite 在 resolveConfig 阶段读取 `VITE_*` 前缀的进程环境变量，
 * 因此开发服务器与生产构建都能通过 `import.meta.env.VITE_APP_VERSION` 读到同一个值。
 * （不要改成 define 一个裸全局量：Vite 6 的 dev 只对构建做静态替换，dev 下会 ReferenceError。）
 */
export function applyAppVersionEnv() {
  process.env.VITE_APP_VERSION ||= APP_VERSION;
  return APP_VERSION;
}
