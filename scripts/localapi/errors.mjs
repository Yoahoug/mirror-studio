// 本地 API 的语义化 HTTP 错误。
//
// 各域自己的错误类型（ValidationError/UploadError/NovelError/ComfyError/
// TtsError/ChatError）统一按 400/自带状态码处理；「行不存在」必须回 404，
// 否则本地模式会把「记录已被删除」报成 500 内部故障。
//
// code 默认跟随 HTTP 状态码（本地模式一贯的 fail() 约定）；只有管理台接口会
// 按线上契约传 404000（CodeConsoleNotFound），因为业务码 404000 只属于 console。
export class NotFoundError extends Error {
  constructor(message, code = 404) {
    super(message);
    this.name = 'NotFoundError';
    this.status = 404;
    this.code = code;
  }
}
