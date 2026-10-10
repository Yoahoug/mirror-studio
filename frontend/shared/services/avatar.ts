/**
 * 本地生成用户头像（data URI SVG），消灭 dicebear 等运行时外链。
 * 以用户名为种子取色 + 画 5x5 对称 identicon，风格与设计系统一致（圆角、单色调）。
 */

const PALETTE = [
  '#6d5dfc', // accent
  '#3b82f6', // info
  '#10b981', // success
  '#f59e0b', // warning
  '#ef4444', // danger
  '#8b5cf6',
  '#0ea5e9',
  '#14b8a6',
];

function hashSeed(seed: string): number {
  let hash = 0;
  for (let i = 0; i < seed.length; i++) {
    hash = (hash * 31 + seed.charCodeAt(i)) | 0;
  }
  return Math.abs(hash);
}

export function localAvatar(seed: string): string {
  const name = seed || 'user';
  const hash = hashSeed(name);
  const color = PALETTE[hash % PALETTE.length];
  const bg = '#f0f2f5';

  // 5x5 对称网格：左三列决定整行
  const cells: boolean[] = [];
  for (let i = 0; i < 15; i++) {
    cells.push(((hash >> i) & 1) === 1);
  }
  const grid: boolean[][] = [];
  for (let row = 0; row < 5; row++) {
    const left = cells.slice(row * 3, row * 3 + 3);
    grid.push([...left, left[1], left[0]]);
  }

  const cellSize = 10;
  const rects = grid
    .map((row, y) =>
      row
        .map((filled, x) =>
          filled
            ? `<rect x="${x * cellSize}" y="${y * cellSize}" width="${cellSize}" height="${cellSize}"/>`
            : ''
        )
        .join('')
    )
    .join('');

  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 50 50"><rect width="50" height="50" rx="12" fill="${bg}"/><g fill="${color}">${rects}</g></svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}
