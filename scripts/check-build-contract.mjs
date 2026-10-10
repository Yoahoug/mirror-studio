#!/usr/bin/env node
// 构建期校验（§3.6-4）：build 结束后运行。
// 1. 检查 4 个契约产物文件名存在（Go 模板按固定路径引用，缺失即坏包——直接 fail，不许静默）。
// 2. 输出各应用 js/css 的 gzip 前后体积表，作为体积预算的可见基准。

import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

const CONTRACT_FILES = [
  'static/workspace/assets/workspace.js',
  'static/workspace/assets/workspace.css',
  'static/console/assets/console.js',
  'static/console/assets/console.css',
];

function byteSize(file) {
  return fs.statSync(file).size;
}

function gzipSize(file) {
  return zlib.gzipSync(fs.readFileSync(file)).length;
}

function fmt(bytes) {
  return `${(bytes / 1024).toFixed(1)} kB`;
}

const missing = CONTRACT_FILES.filter((file) => !fs.existsSync(path.join(ROOT, file)));
if (missing.length) {
  console.error('\n✗ 构建产物契约校验失败，缺少：');
  for (const file of missing) console.error(`  - ${file}`);
  console.error('Go 模板按固定路径引用这些文件（frontend/shared/vite.config.base.ts 的产物命名契约）。');
  process.exit(1);
}

console.log('\n✓ 4 个契约产物齐全：');
let table = '  产物                                    原始        gzip\n';
for (const file of CONTRACT_FILES) {
  const full = path.join(ROOT, file);
  table += `  ${file.padEnd(38)} ${fmt(byteSize(full)).padStart(9)}  ${fmt(gzipSize(full)).padStart(9)}\n`;
}
console.log(table);
