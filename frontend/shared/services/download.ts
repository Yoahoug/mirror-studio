// 浏览器落盘下载：用一个带 download 属性的临时 <a> 触发，同源 URL 会直接保存，
// 而不是把音频/图片导航到浏览器内置播放器页。
// 服务端对 /downloads/{token}/?download=1 也会回 attachment，两边是双保险：
// 前端属性决定文件名，服务端头兜住「没有 download 属性」的调用点（如 window.open）。
export function triggerBrowserDownload(url: string, filename?: string): void {
  if (!url) return;
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.rel = 'noopener';
  if (filename) anchor.download = filename;
  anchor.style.display = 'none';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

/** 给已带 query 的地址补参数（历史记录里的图片地址可能已经带 preview=1）。 */
export function appendQueryParam(url: string, key: string, value: string): string {
  if (!url) return url;
  const separator = url.includes('?') ? '&' : '?';
  return `${url}${separator}${encodeURIComponent(key)}=${encodeURIComponent(value)}`;
}
