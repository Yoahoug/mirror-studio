# 0.0.1 Go 服务内部架构（实现规范）

本文件是各域模块实现的**唯一接口约定**。新增代码一律遵守，不得自行发明跨层约定。

## 1. 分层

```
httpapi  → 路由、鉴权、序列化/校验（只做 HTTP）
service  → 业务规则（generator/service.go、media/service.go …）
store    → 手写 SQL（generator/store.go、media/store.go …）
provider → 外部渠道（generator/provider.go、novel/llm.go），只被 service 调用
```

- **HTTP handler 里不得出现 SQL**；**store 里不得出现 HTTP**。
- 跨域调用只能通过对方的 service 公开函数（例如 quota 被 generator 调用走
  `quota.Service.Freeze`），不得直接读对方的表。
- 每个域的 `Register(mux *http.ServeMux, deps Deps)` 注册自己的路由，由
  `main.go` 统一调用。

## 2. 包布局

| 包 | 职责 |
| --- | --- |
| `internal/config` | 唯一的环境变量入口（已实现） |
| `internal/crypt` | Fernet 字段加密 + Django Signer/TimestampSigner（已实现） |
| `internal/db` | SQLite 连接、嵌入式迁移、`WithImmediate` 事务（已实现） |
| `internal/httpapi` | 响应信封、错误码、CSRF/会话、中间件、静态资源 |
| `internal/siteconfig` | 配置读写：DB → 环境变量 → 默认值，60s 缓存 |
| `internal/auth` | 用户、密码哈希、会话登录、注册/登录/找回、账号设置 |
| `internal/risk` | IP 封禁、敏感词、登录失败与任务限流计数 |
| `internal/quota` | 额度账户与账本（冻结/消费/释放/管理员调整） |
| `internal/generator` | 生成任务状态机、渠道分发、worker、主题、历史 |
| `internal/media` | 图片库、分片上传、预览、签名下载、SSRF 远程图、清理 |
| `internal/novel` | EPUB 解析、场景分析、场景出图、SSE |
| `internal/console` | 运营控制台 API |
| `internal/audit` | 回调事件日志 |
| `internal/web` | 服务端渲染页面（模板引擎 + 表单渲染） |

## 3. 响应与错误

只用 `internal/httpapi` 的信封函数，禁止手写 JSON 字符串：

```go
httpapi.OK(w, data)                                        // {"code":0,"message":"ok","data":...}
httpapi.Success(w, http.StatusCreated, data, "ok")         // 201
httpapi.Error(w, status, code, message)                    // 失败
httpapi.BadRequest(w, "提示词不能为空")                      // 400 / 400001
```

业务码常量见 `internal/httpapi/respond.go`。文案必须与
`docs/api-contract-0.0.1.md` 逐字一致（含标点与空格）。

表单错误用 `httpapi.FormErrors`，顺序 = 参考实现的字段声明顺序：

```go
errs := httpapi.NewFormErrors()
errs.Add("username", "账号已存在", "")
errs.Add("", "两次密码输入不一致", "")
BadRequest(w, errs.AsJSON())   // message 是 JSON 字符串
```

## 4. 请求上下文

```go
state := httpapi.From(r)        // RequestID / ClientIP / Session / CSRF / User / Body
httpapi.UserOf(r)               // httpapi.Principal 或 nil
httpapi.SessionOf(r)            // *httpapi.Session
payload, err := httpapi.ParsePayload(r)
```

- `state.ClientIP` 已按 `TRUST_X_FORWARDED_FOR` 解析，**不要**再自己读
  `X-Forwarded-For`。
- `err == httpapi.ErrRequestTooBig` 时，生成域返回 400100
  `参考图过大，请压缩后再试`，其它域返回 400 HTML。
- 需要登录的 handler 自己判断 `httpapi.UserOf(r) == nil` 并返回
  `302 /workspace?auth=login&next=<path>`（页面）或让中间件处理（API 用 302 与参考一致）。

## 5. 数据库

- 所有 SQL 用 `?` 占位符，手写。
- 读写序列必须原子时用 `db.WithImmediate`：

```go
err := h.DB.WithImmediate(ctx, func(tx *db.Tx) error {
    // SELECT ...; UPDATE ...; INSERT ...
    return nil
})
```

- 时间一律 `db.FormatTime(t)`（UTC，`2006-01-02 15:04:05.999999`）；
  日期列用 `db.FormatDate(t)`；读取用 `db.ParseTime`。
- 布尔列写 `db.Bool(x)`（0/1），读用 `sql.NullInt64` 或 `int`。
- 可空列写 `db.NullableString` / `db.NullableTime`。
- **表名列名不得改动**：`SELECT *` 快照与参考实现逐列比对。

## 6. 会话与 CSRF

会话值只允许基础类型（字符串/整数）。参考实现用到的键：

| 键 | 语义 |
| --- | --- |
| `user_id` | 登录用户主键（int64） |
| `captcha_code` | 当前验证码（大写） |

登录成功后必须 `session.CycleKey()`；退出 `httpapi.ClearSession(w, session)`。
需要下发 CSRF Cookie 时调用 `httpapi.SetCSRFCookie(w)`。

## 7. 配置读取

```go
cfg := siteconfig.New(db, env, logger)     // 单例，由 main 注入
value := cfg.String("site.brand_name", "绘镜")
flag := cfg.Bool("email.verification_enabled", false)
n := cfg.Int("site.daily_user_task_limit", 50)
```

解析顺序固定：`core_siteconfig` 行（非空）→ 环境变量 → 默认值，60 秒缓存。
风控参数（`risk.*`）**必须**走 siteconfig（见 review A-2），
不得直接读环境变量。

## 8. 外部请求

- 每个 provider 客户端必须有超时（渠道 `timeout_seconds`），禁止无超时请求。
- 重试只允许在 `_is_retryable_error` 等价条件下（HTTP 状态为空、≥500、
  或 ∈ {408,409,425,429}），次数上限用渠道 `max_retries`，退避
  `min(1.5*attempt, 3)` 秒。
- 错误分类：超时 → 400504 语义、无渠道 → 400503、其余 → 任务失败终态。
- **密钥不得进日志**：记录渠道请求时只记 host、路径、状态码、耗时。

## 9. 并发与后台循环

- worker 用 goroutine + 带缓冲 channel；任务认领必须是
  `UPDATE ... WHERE status = ... RETURNING` 或 `WithImmediate`，禁止
  “先 SELECT 再 UPDATE”。
- 每个后台循环顶端必须有 `defer recover`，出错记日志并退避重试。

## 10. 测试

每个域至少覆盖：
- 参考实现对应 asserts 的翻译（见 `apps/*/tests.py`）。
- 并发用例：任务认领、额度冻结/消费、分片完成幂等。

`go test ./...` 必须全绿，任何一域的新增失败都不允许合入。

## 11. 跨域 API（必须逐字使用这些签名，不得改名）

各域并行实现，因此下表是冻结的跨域接口。产生方负责实现，消费方按此调用。

### `internal/auth`

```go
type User struct {                    // 实现 httpapi.Principal
    ID        int64
    Username  string
    Email     string
    Password  string
    Role      string   // "user" | "admin"
    Status    string   // "active" | "disabled"
    IsStaff   bool
    IsSuperuser bool
    DateJoined time.Time
    LastLogin  *time.Time
    LastLoginIP string
    LastLoginAt *time.Time
    IsActiveFlag bool
}
func (u *User) IDValue() int64          // 供 Principal 使用（见下）
func (u *User) IsOperator() bool
func (u *User) IsActiveStatus() bool

type Profile struct { Nickname, Avatar, RegisterIP, RegisterSource, Remark string }

type Service struct { ... }
func NewService(db *db.DB, cfg *config.Config, cipher *crypt.FieldCipher, site *siteconfig.Service, log *slog.Logger) *Service
func (s *Service) ResolvePrincipal(ctx context.Context, userID int64) (httpapi.Principal, error)
func (s *Service) ByID(ctx context.Context, id int64) (*User, error)
func (s *Service) ByUsername(ctx context.Context, username string) (*User, error)
func (s *Service) ProfileOf(ctx context.Context, userID int64) (Profile, error)
func (s *Service) Authenticate(ctx context.Context, username, password string) (*User, error)
func (s *Service) Register(ctx context.Context, username, email, password, ip string) (*User, error)
func (s *Service) SetPassword(ctx context.Context, userID int64, password string) error
func (s *Service) UpdateLogin(ctx context.Context, userID int64, ip string) error
func (s *Service) Summary(ctx context.Context, user *User) (map[string]any, error)   // 不含 quota
func (s *Service) EnsureQuotaAccount(ctx context.Context, userID int64) error          // 内部用 quota.Service
func HashPassword(password string) (string, error)
func VerifyPassword(encoded, password string) (bool, bool)   // (ok, needsRehash)
func ValidatePassword(password string, user *User) []FieldError  // 见 §3 文案

func NewHandlers(svc *Service, site *siteconfig.Service, cfg *config.Config, log *slog.Logger) *Handlers
func (h *Handlers) Register(mux *http.ServeMux)
```

`httpapi.Principal` 的方法名为：`ID() int64`、`Username() string`、`IsOperator() bool`、
`IsActiveStatus() bool`、`Email() string`。`auth.User` 直接实现这五个方法
（字段 `ID` 与方法 `ID()` 冲突，因此字段改名为 `IDValue`，方法为 `ID() int64`）。

### `internal/quota`

```go
type Account struct {
    ID int64; UserID int64; DailyQuota int; AvailableQuota int; FrozenQuota int
    TotalGeneratedCount int; LastResetDate string
}
type Service struct { ... }
func NewService(db *db.DB, cfg *config.Config, site *siteconfig.Service, log *slog.Logger) *Service
func (s *Service) Summary(ctx context.Context, userID int64) (map[string]any, error)
func (s *Service) Ensure(ctx context.Context, userID int64) (*Account, error)
func (s *Service) Refresh(ctx context.Context, userID int64) (*Account, error)
func (s *Service) Freeze(ctx context.Context, userID int64, amount int, taskNo string) (*Account, error)
func (s *Service) Consume(ctx context.Context, userID int64, amount int, taskNo string) error
func (s *Service) Release(ctx context.Context, userID int64, amount int, taskNo string) error
func (s *Service) AdminAdjust(ctx context.Context, userID int64, dailyQuota, availableQuota *int, remark string) (*Account, error)
func (s *Service) PendingTaskLimit(ctx context.Context) int
```
`Summary` 返回键：`daily_quota/available_quota/frozen_quota/total_generated_count/last_reset_date`。

### `internal/risk`

```go
type Service struct { ... }
func NewService(db *db.DB, site *siteconfig.Service, cfg *config.Config, log *slog.Logger) *Service
func (s *Service) IPBlocked(ctx context.Context, ip string) (bool, string)   // (blocked, message)
func (s *Service) EnsureLoginAllowed(ctx context.Context, username, ip string) error   // 返回带文案的 error
func (s *Service) Failures(ctx context.Context, username, ip string) int64
func (s *Service) RecordLoginFailure(ctx context.Context, username, ip string) int64
func (s *Service) ClearLoginFailures(ctx context.Context, username, ip string)
func (s *Service) EnsurePromptSafe(ctx context.Context, prompt string) error
func (s *Service) ConsumeTaskQuota(ctx context.Context, userID int64) error   // 返回文案 error 表示 400403
func (s *Service) Counters() *Counters                                        // 供中间件限流复用
type Counters struct { ... }
func (c *Counters) Increment(key string, window time.Duration) int64
```

### `internal/media`

```go
type Image struct {
    ID int64; TaskID int64; UserID int64; TaskNo string; Prompt string
    Status string; Width int; Height int; FileSize int64; MimeType string
    ObjectKey string; BucketName string; PreviewObjectKey string
    CreatedAt time.Time; ExpiresAt time.Time
}
type Service struct { ... }
func NewService(db *db.DB, cfg *config.Config, site *siteconfig.Service, log *slog.Logger) *Service
func (s *Service) StoreGeneratedImage(ctx context.Context, in StoreImageInput) (*Image, error)
type StoreImageInput struct {
    UserID, TaskID int64; TaskNo string; Index int
    Content []byte; MimeType string; Prompt string
}
func (s *Service) StoreReferenceImage(ctx context.Context, userID int64, taskNo string, content []byte, mimeType string) (map[string]string, error)
func (s *Service) ReadObject(ctx context.Context, bucket, key string) ([]byte, error)
func (s *Service) IssueDownloadURL(ctx context.Context, image *Image, userID int64, inline, preview bool) (string, error)
func (s *Service) ResolveDownloadToken(ctx context.Context, token string, maxAge time.Duration) (*Image, error)
func (s *Service) BuildDownloadResponse(ctx context.Context, image *Image, inline, preview bool) (content []byte, contentType, disposition, cacheControl string, err error)
func (s *Service) FetchRemoteImage(ctx context.Context, url string) ([]byte, string, error)
func (s *Service) CleanupStorage(ctx context.Context, limit int) (map[string]int, error)
func (s *Service) NewHandlers() *Handlers
func (h *Handlers) Register(mux *http.ServeMux)
```

### `internal/generator`

```go
type Service struct { ... }
func NewService(deps Deps) *Service
type Deps struct {
    DB *db.DB; Config *config.Config; Site *siteconfig.Service
    Risk *risk.Service; Quota *quota.Service; Media *media.Service
    Audit *audit.Service; Cipher *crypt.FieldCipher; Log *slog.Logger
}
func (s *Service) StartWorker(ctx context.Context)     // 启动后台 worker goroutine
func (s *Service) StopWorker()
func (s *Service) Submit(ctx context.Context, in SubmitInput) (*Task, error)
type SubmitInput struct {
    UserID int64; ClientIP string; Prompt, NegativePrompt, StyleCode, SizeCode, QualityCode string
    GenerationMode, ReferenceImageDataURL, ReferenceImageName, ReferenceUploadToken string
    TopicID *int64; ClientRequestID string
}
func (s *Service) TaskByNo(ctx context.Context, userID int64, taskNo string) (*Task, error)
func (s *Service) Cancel(ctx context.Context, userID int64, taskNo string) (*Task, error)
func (s *Service) DeleteHistory(ctx context.Context, userID int64, taskNo string) error
func (s *Service) SerializeTask(t *Task, userID int64, includeURLs bool) map[string]any
func (s *Service) NewHandlers() *Handlers
func (h *Handlers) Register(mux *http.ServeMux)
// 供 novel / console / bootstrap 使用
func (s *Service) ResolveChannel(ctx context.Context, code string) (*Channel, error)
func (s *Service) EnsureDefaultChannels(ctx context.Context) error
```

### `internal/novel`

```go
type Service struct { ... }
func NewService(deps Deps) *Service
type Deps struct {
    DB *db.DB; Config *config.Config; Site *siteconfig.Service
    Generator *generator.Service; Media *media.Service; Log *slog.Logger
}
func (s *Service) StartWorker(ctx context.Context)
func (s *Service) StopWorker()
func (s *Service) NewHandlers() *Handlers
func (h *Handlers) Register(mux *http.ServeMux)
```

### `internal/console`

```go
func NewService(deps Deps) *Service
type Deps struct {
    DB *db.DB; Site *siteconfig.Service; Config *config.Config
    Auth *auth.Service; Quota *quota.Service; Generator *generator.Service
    Media *media.Service; Log *slog.Logger
}
func (s *Service) NewHandlers() *Handlers
func (h *Handlers) Register(mux *http.ServeMux)
```

### `internal/audit`

```go
type Service struct { ... }
func NewService(db *db.DB, log *slog.Logger) *Service
func (s *Service) LogCallbackEvent(ctx context.Context, eventType, source string, payload map[string]any, success bool, remark string) error
func (s *Service) RecentFailures(ctx context.Context, limit int) ([]map[string]any, error)
```

### `internal/web`

```go
func NewService(deps Deps) *Service
type Deps struct {
    DB *db.DB; Config *config.Config; Site *siteconfig.Service
    Auth *auth.Service; Quota *quota.Service; Media *media.Service; Log *slog.Logger
}
func (s *Service) Register(mux *http.ServeMux)
```
