// Vite 注入的应用版本号：唯一来源是仓库根 VERSION
// （scripts/release.py 是它的唯一写入者，见 frontend/shared/app-version.mjs）。
// 本地单端口 dev server 与生产构建共用同一份定义。
//
// 注意：本文件与 app-version.mjs 同名，会被 TypeScript 当作该模块的类型声明，
// 因此 mjs 里的导出必须在这里同步声明；ImportMetaEnv 走 declare global。
export function readAppVersion(): string;
export function applyAppVersionEnv(): void;

declare global {
  interface ImportMetaEnv {
    readonly VITE_APP_VERSION?: string;
  }

  interface ImportMeta {
    readonly env: ImportMetaEnv;
  }
}
