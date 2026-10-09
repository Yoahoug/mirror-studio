# 绘镜 0.0.1 接口契约（冻结于 Django 1.x 基线）

本文件是 0.0.1 (Go) 重写的行为契约。来源：`frontend/workspace/src/services/apiService.ts`、
`frontend/console/src/services/apiService.ts`、全部 `apps/*/urls.py`、全部 `apps/*/views.py` 与
`config/urls.py`，以及 `.reftmp/run-ref/` 中由 Python 基线真实录制并归一化的 HTTP 轨迹
（main 211 步 / email 22 步 / upstream 77 步）。

约定：**本文件描述的是"必须逐字节/逐字段保持"的对外契约**。凡是本文件未列出的字段，Go 端不得新增；
凡是本文件列出的字段，Go 端不得缺失或改名。

---

## 0. 传输层与通用约定

### 0.1 凭据模式

| 项 | 值 |
| --- | --- |
| 会话 Cookie | `sessionid`；`HttpOnly`；`SameSite=Lax`；生产 `Secure` |
| CSRF Cookie | `csrftoken`；**非** `HttpOnly`；`SameSite=Lax`；生产 `Secure` |
| 前端凭据模式 | SPA 全部请求 `fetch(..., {credentials: "include"})`（同源）；SSR 表单同样依赖 Cookie 会话 |
| 写请求 CSRF 头 | `X-CSRFToken: <csrftoken 值>`，仅非安全方法（POST/PUT/PATCH/DELETE）需要 |
| 请求追踪头（响应） | `X-Request-Id`：回显请求头 `X-Request-Id`，缺失时生成 `uuid4().hex[:16]` |
| 其他响应头 | `X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`（全站，含 API） |

CSRF 失败：Django 标准 403 HTML 页面（非 JSON）。前端不解析该响应体，只按状态码 403 处理。

### 0.2 业务响应信封

成功：

```json
{"code": 0, "message": "ok", "data": <payload>}
```

失败：

```json
{"code": <int>, "message": "<人类可读中文>", "data": <payload 或 null>}
```

JSON 编码为 Django `JsonResponse` 默认风格：`ensure_ascii=True`（非 ASCII 转 `\uXXXX`）、
分隔符 `", "` 与 `": "`。**Go 端必须复刻该编码风格**，否则差分验证出现全量文本差异。

### 0.3 业务码表（全部实测确认）

| code | HTTP | 含义 | 出现位置 |
| --- | --- | --- | --- |
| 0 | 200/201 | 成功 | 全局 |
| 400000 | 400 | 通用请求错误（默认） | 全局 |
| 400001 | 400 | 参数缺失/非法 | 全局 |
| 400002 | 400 | 业务规则冲突（唯一性、余额不足等） | 全局 |
| 400003 | 400 | 状态不允许 | 全局 |
| 400100 | 400 | 表单校验失败（`message` 为 Django 错误 JSON 字符串） | `/api/auth/*`、`/api/account/password` |
| 400403 | 403 | 权限不足 | 业务 API |
| 400404 | 404 | 资源不存在 | 业务 API |
| 400429 | 429 | 触发限流 | 生成任务限流 |
| 400501 | 501 | 上游不支持 | 生成 |
| 400503 | 503 | 服务不可用（无可用渠道） | `/api/chat`、生成 |
| 400504 | 504 | 上游超时 | 生成 |
| 401000 | 401 | 未登录（console） | `/console/api/` |
| 401005 | 401 | 内部推送签名失败 | 已废弃接口 |
| 403000 | 403 | 非运营人员（console） | `/console/api/` |
| 403001 | 403 | 账号状态不可用（console） | `/console/api/` |
| 404000 | 404 | 资源不存在（console） | `/console/api/` |
| 429000 | 429 | 频率限制（console） | `/console/api/` |
| 500001 | 500 | 邮件发送失败 | `/api/auth/register/email-code` |
| 500002 | 500 | 邮件后端未配置 | `/api/auth/register/email-code` |

### 0.4 表单校验错误载荷（400100）

`message` 是 **JSON 字符串**（非对象），形如：

```
{"username": [{"message": "这个字段是必填项。", "code": "required"}]}
```

前端 `readableError()` 的逻辑：`JSON.parse(message)` → `Object.keys(parsed)[0]` 取第一个字段 →
`parsed[field][0].message`。因此字段顺序、`message` 文案、`code` 字段都必须保持。

已实测必须复刻的 Django zh-hans 文案：

| 场景 | message | code |
| --- | --- | --- |
| 必填缺失 | `这个字段是必填项。` | `required` |
| 邮箱格式 | `输入一个有效的 Email 地址。` | `invalid` |
| 超长 | `确保该变量包含不超过 150 字符 (目前字符数 160)。` | `max_length` |
| 密码过短 | `This password is too short. It must contain at least 8 characters.` | `password_too_short` |
| 密码过于常见 | `这个密码太常见了。` | `password_too_common` |
| 密码全数字 | `密码只包含数字。` | `password_entirely_numeric` |
| 密码与用户名相似 | `密码跟 用户名 太相似了。` | `password_too_similar` |

应用自定义（`code` 为空字符串 `""`）：`两次密码输入不一致`、`至少提供账号或邮箱`、
`账号或密码错误`、`用户不存在`。

### 0.5 分页形态

业务列表（`/api/generate/tasks`、`/api/workspace/history`、`/api/images`、`/api/novelviz/tasks`）：

```json
{"code":0,"message":"ok","data":{"items":[...],"total":37,"page":1,"page_size":10,"total_pages":4}}
```

`page` 默认 1，`page_size` 默认 10，`page_size` 钳制到 `1..100`（实测 `page_size=999` 返回 100 条）。

Console 列表（`/console/api/users/`、`/console/api/tasks/`）：同一形态，`per_page` 参数名不同，
默认 20，钳制 `1..100`。

### 0.6 静态资源路径（前端零改动硬约束）

| 资源 | 路径 | 说明 |
| --- | --- | --- |
| 工作台入口 JS | `/static/workspace/assets/workspace.js` | 实际引用带 `?v=<mtime_ns>` 查询串 |
| 工作台 CSS | `/static/workspace/assets/workspace.css` | |
| 控制台入口 JS | `/static/console/assets/console.js` | |
| 控制台 CSS | `/static/console/assets/console.css` | |
| 站点样式 | `/static/css/main.css` | SSR 页面使用 |
| 图标 | `/static/favicon.svg` | |
| 媒体文件 | `/media/...`（仅 DEBUG 时由 Django 直供） | 生产经签名 URL 读取 |

---

## 1. 页面路由（SSR 与 SPA 外壳）

| 路径 | 方法 | 视图 | 状态码 | 说明 |
| --- | --- | --- | --- | --- |
| `/` | GET | RedirectView | 302 → `/workspace` | |
| `/workspace` | GET | WorkspaceView | 200 | 输出 SPA 外壳 `workspace/index.html`；未登录且开启验证码时注入 `CAPTCHA_CODE`/`CAPTCHA_SVG` 并写入 session |
| `/console` | GET | ConsoleView | 302 未登录 → `/workspace?auth=login`；403 无权限 | 运营控制台外壳 |
| `/console/` | GET | ConsoleView | 同上 | |
| `/admin` | GET | RedirectView | 302 → `/console/` | |
| `/admin/` | GET | RedirectView | 302 → `/console/` | |
| `/healthz` | GET | HealthCheckView | 200 `{"status":"ok"}` | |
| `/login` | GET | LoginPageView | 200 / 302 已登录 → `/workspace` | |
| `/register` | GET | RegisterPageView | 200 / 302 已登录 → `/workspace` | |
| `/forgot-password` | GET | ForgotPasswordPageView | 200 | |
| `/account` | GET | AccountDashboardView | 200 / 302 → `/workspace?auth=login&next=/account` | |
| `/account/security` | GET | SecurityPageView | 200 / 302 | |
| `/account/history` | GET | HistoryPageView | 200 / 302 | |
| `/account/history/<task_no>` | GET | HistoryDetailPageView | 200 / 404 | |
| `/pricing` | GET | **未路由** | **404** | 见 `docs/review-0.0.1.md` P1-1；0.0.1 必须保持 404 |
| `/downloads/<token>/` | GET | SignedDownloadView | 200 / 403 / 404 | 签名下载，见 §7.4 |

`LOGIN_URL = /workspace?auth=login`、`LOGIN_REDIRECT_URL = /workspace`。
未登录访问受保护页：302 到 `/workspace?auth=login&next=<原路径>`。
SPA 的 console 页未登录：302 到 `/workspace?auth=login&next=/console/`。

---

## 2. 认证与会话（`apps/users`）

### 2.1 `GET /api/auth/csrf`

无鉴权。设置 `csrftoken` Cookie（若不存在）并返回其值。

```
200 {"code":0,"message":"ok","data":{"csrf_token":"<64字符>"}}
```

### 2.2 `GET /api/auth/captcha`

无鉴权。返回新的验证码并将其写入 session。

```
200 {"code":0,"message":"ok","data":{"captcha_code":"<4位>","captcha_svg":"data:image/svg+xml;base64,..."}}
```

验证码字母表 `23456789ABCDEFGHJKLMNPQRSTUVWXYZ`，长度 4，大小写不敏感比较（比较前 `upper()`）。

### 2.3 `POST /api/auth/login`

请求（JSON 或表单）：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `username` | ✅ | |
| `password` | ✅ | |
| `captcha_code` | 条件 | `LOGIN_CAPTCHA_ENABLED=1` 时必填；从 session 取 `captcha_code` 比对 |

成功：

```
200 {"code":0,"message":"ok","data":{"next":"/workspace","user":{"username":...,"nickname":...}}}
```

`next` 来源优先级：请求体 `next` → 请求头 `Referer` 的 path → `LOGIN_REDIRECT_URL`；
且只接受站内相对路径（外站 URL 回落到 `/workspace`）。

失败分支（实测）：

| 场景 | HTTP | code | message |
| --- | --- | --- | --- |
| CSRF 缺失 | 403 | — | Django CSRF HTML |
| 表单校验失败 | 400 | 400100 | 表单错误 JSON |
| 验证码错误 | 400 | 400100 | `{"captcha_code":[{"message":"验证码不正确","code":""}]}` |
| 凭据错误 | 400 | 400100 | `{"__all__":[{"message":"账号或密码错误","code":""}]}` |
| 风控锁定 | 403 | 400403 | `登录失败次数过多，请在 600 秒后重试` |
| IP 封禁 | 403 | 400403 | `当前 IP 已被限制访问` |

登录成功副作用：`last_login_at=now()`、`last_login_ip=<客户端IP>`、清空该 `(username, ip)` 登录失败计数、
轮换 session id。

### 2.4 `POST /api/auth/logout`

需登录 + CSRF。清空 session。

```
302 → /workspace          （未登录时也返回 302，幂等）
```

### 2.5 `POST /api/auth/register/check`

无鉴权，CSRF 需要。请求 `{"username": "...", "email": "..."}`（可只传其一，但**至少一个**）。

```
200 {"code":0,"message":"ok","data":{"username_available":true,"email_available":true}}
400 400001 "至少提供账号或邮箱"
```

### 2.6 `POST /api/auth/register/email-code`

无鉴权，CSRF 需要。请求 `{"username": "...", "email": "..."}`（**两者都必填**）。

行为：校验可用性 → 生成 `email.verification_code_length`（默认 6）位数字码 → TTL
`email.verification_ttl_seconds`（默认 600s）→ 重发冷却 `email.verification_resend_seconds`（默认 60s）→
每日每邮箱 `email.daily_limit_per_email`（默认 10）、每 IP `email.daily_limit_per_ip`（默认 30）。
验证码在存储前 `sha256` 摘要，不进日志。

```
200 {"code":0,"message":"验证码已发送","data":{"expires_in":600,"resend_after":60}}
400 400001 "两次请求间隔过短，请 42 秒后重试"
400 400001 "该邮箱今日发送次数已达上限"
500 500002 "邮件服务未配置"
500 500001 "邮件发送失败"
```

`email.verification_enabled=0` 时跳过校验（注册时不要求验证码）。

### 2.7 `POST /api/auth/register`

无鉴权，CSRF 需要。

| 字段 | 必填 |
| --- | --- |
| `username` | ✅ |
| `email` | ✅ |
| `password1` | ✅ |
| `password2` | ✅ |
| `verification_code` | 条件（`email.verification_enabled=1` 时） |

成功：自动登录并

```
200 {"code":0,"message":"注册成功","data":{"next":"/workspace","user":{...}}}
```

失败：400100 + 表单错误 JSON；验证码错误 `{"verification_code":[{"message":"验证码不正确或已过期","code":""}]}`。

### 2.8 `POST /api/auth/reset-password`

无鉴权，CSRF 需要。字段 `identifier`（用户名或邮箱）、`verification_code`、`password1`、`password2`。

```
200 {"code":0,"message":"密码已重置","data":null}
400 400001 "用户不存在"
400 400100 表单错误 JSON
```

### 2.9 `GET /api/account/summary`

需登录。

```json
{"code":0,"message":"ok","data":{
  "user":{"username","nickname","email","avatar","remark"},
  "quota":{"daily_quota":20,"available_quota":20,"frozen_quota":0,
           "total_generated_count":0,"last_reset_date":"2026-10-09"},
  "recent_tasks":[<最多 5 条任务摘要>]}}
```

### 2.10 `GET /api/account/settings` / `PATCH /api/account/settings`

需登录；PATCH 需 CSRF。可写字段：`nickname`、`avatar`、`remark`。响应 `data` 为
`{"user":{username,nickname,email,avatar,remark,role,status,register_source,date_joined,last_login_at,last_login_ip},"quota":{...}}`。

`role` 输出本地化文案（`普通用户`/`管理员`…），`status` 输出 `正常` 等。PATCH 成功 `message = "资料已更新"`。

### 2.11 `POST /api/account/password`

需登录 + CSRF。字段 `old_password`、`password1`、`password2`。

```
200 {"code":0,"message":"密码已更新","data":null}
400 400100 表单错误 JSON
```

---

## 3. 风控（`apps/risk`）

无独立 HTTP 接口，全部以中间件/服务形式挂在其它请求上。参数**只读 `settings.RISK_*`**
（console 里可见的 `risk.*` SiteConfig 项是死配置，见 `docs/review-0.0.1.md` P2-1）。

| 机制 | 键 | 默认 | 触发结果 |
| --- | --- | --- | --- |
| 登录失败计数 | `risk:login:{username}:{ip}` | 窗口 600s，阈值 5 | 403 `登录失败次数过多，请在 {ttl} 秒后重试` |
| 登录成功清零 | 同上 | — | 删除计数 |
| 任务提交限流 | `risk:task:{user_id}` | 窗口 60s，阈值 3 | 429 400429 `操作过于频繁，请稍后再试` |
| IP 访问规则 | `risk_ipaccessrule` 表 | 无 | 403 `当前 IP 已被限制访问`（受 `is_effective` 与 `expires_at` 约束） |
| 敏感词 | `risk_sensitivewordrule` 表 | 无 | 400 400001 `提示词包含敏感词：{词}` |
| 客户端 IP 解析 | `TRUST_X_FORWARDED_FOR` | 生产 1 | 为真取 `X-Forwarded-For` 最左值，否则 `REMOTE_ADDR` |

---

## 4. 额度（`apps/quota`，原 `apps/billing`）

| 配置 | 默认 | 说明 |
| --- | --- | --- |
| `daily_quota` | 20 | 每日免费额度；跨天首次访问时重置（按本机时区 `localdate`） |
| 每日任务上限 | 20 | 与 `daily_quota` 同源 |
| `pending` 任务上限 | 3 | 同时处于 queued/running 的任务数 |
| 账本号 | `L%Y%m%d%H%M%S` + `token_hex(4).upper()` | `billing_quotaledger.ledger_no` |
| 变更类型 | `reset` / `freeze` / `consume` / `unfreeze` / `admin_adjust` / `daily_quota_update` | |
| 方向 | `in` / `out` / `none` | |

冻结-消费-释放语义：提交任务即冻结 1 次并写 `freeze` 账本；成功写 `consume`（`out`）；失败/取消写
`unfreeze`（`in`）。任务 `quota_finalized` 置位保证只结算一次。

---

## 5. 生成（`apps/generator`）

### 5.1 `GET /api/generate/topics` / `POST /api/generate/topics`

`data` 为数组 `[{id,title,cover_image_id,last_task_at}]`。POST 请求 `{title, cover_image_id?}`。

### 5.2 `POST /api/generate/reference-images`

需登录 + CSRF。`multipart/form-data`，字段 `file`（PNG/JPEG，≤ 10MB）。返回

```
200 {"code":0,"message":"ok","data":{"token":"<签名串>","name":"fruit.png","size":1234,"width":512,"height":512,"data_url":"data:image/png;base64,..."}}
```

`token` 用 Django `TimestampSigner(salt="generator.reference-upload")` 生成，提交任务时用于
`reference_token` 字段（避免重复上传）。

### 5.3 `POST /api/generate/tasks`

需登录 + CSRF + 风控限流 + 额度。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `prompt` | ✅ | 敏感词校验 |
| `topic_id` | ❌ | |
| `size_code` | ✅ | 如 `1024x1024`、`2048x1152`；规范化规则见下 |
| `quality_code` | ✅ | `auto`/`low`/`medium`/`high` |
| `style_code` | ✅ | 默认 `general` |
| `negative_prompt` | ❌ | |
| `generation_mode` | ❌ | `text_to_image`（默认）/ `image_to_image` |
| `reference_token` | i2i 必填 | 来自 §5.2 |
| `client_request_id` | ❌ | 幂等键，(user, client_request_id) 唯一 |

尺寸规范化：边长对齐 16px 的倍数，最大边 ≤ 2048，长宽比 ≤ 3:1，像素总数 ≥ 655360；
非法时 400 400001。

成功：

```
200 {"code":0,"message":"ok","data":<serialize_task 结果>}
```

`serialize_task` 字段（实测全量）：

```json
{"task_no","topic_id","prompt","negative_prompt","size_code","quality_code","style_code",
 "generation_mode","reference_image_attached","reference_image_name","reference_image_data_url",
 "status","progress_percent","quota_cost","error_code","error_message",
 "submitted_at","started_at","finished_at",
 "images":[{"id","width","height","status","download_url","preview_url","expires_at"}]}
```

失败分支：400429 限流、400 400001 敏感词/尺寸非法、400 400002 额度不足或并发 pending 超限、
400 400503 无可用渠道、400 400003 幂等命中正在执行的任务。

### 5.4 `GET /api/generate/tasks`

查询参数：`page`、`page_size`、`q`（提示词模糊）、`status`。分页形态见 §0.5。

### 5.5 `GET /api/generate/tasks/{task_no}`

200 同 `serialize_task`；404 400404 `任务不存在`。

### 5.6 `POST /api/generate/tasks/{task_no}/cancel`

200 同 `serialize_task`（`status="canceled"`）；404 400404；400 400003 状态不允许取消。
取消同时释放冻结额度。

### 5.7 `GET /api/workspace/history`

需登录。合并生成任务与小说任务为统一历史列表（分页形态同 §0.5）。小说任务序列化字段：
`{"kind":"novel","id","book_filename","book_source","scene_mode","status","progress_percent","error_message","created_at","finished_at","images":[{"id","scene_number","title","image_url","preview_url","download_url","status"}]}`。

### 5.8 任务状态机

状态：`queued` → `running` → `succeeded` / `failed` / `canceled`。

- 认领：原子领取（Django 用 `select_for_update(skip_locked)`，Go 用 `UPDATE ... RETURNING` 或
  `BEGIN IMMEDIATE`）；置 `running`，`execution_attempts += 1`，`lease_expires_at = now + lease`。
- 重试：仅当错误可重试（HTTP status 为 None、≥500、或 ∈ {408,409,425,429}）且
  `execution_attempts < max_retries`；重试前 `sleep(min(1.5 * attempt, 3))` 秒。
- 失败终态：`error_code`、`error_message`、`finished_at` 置位并释放冻结额度。
- 超时/卡死回收：`lease_expires_at` 过期且仍 `running` 的任务被重新入队。
- 用户可读失败原因统一为 `PUBLIC_FAILURE_MESSAGE`（不泄漏上游细节）。

### 5.9 渠道分发与外部协议（`provider` 层）

`provider_vendor` ∈ `openai` / `siliconflow`，二者请求体不同：

- **openai** 文生图：`POST {base_url}{api_path}` JSON `{model, prompt, size, quality, n, response_format:"b64_json", ...}`。
  图生图优先 `multipart/form-data` 走 `edit_api_path`（未配置则 `api_path`），字段
  `image`（文件）、`prompt`、`size`、`model`；上游返回 400/404/405/415/422 时降级为 JSON
  `{"image_url": "data:image/png;base64,..."}` 重试一次。
- **siliconflow**：JSON `{model, prompt, image_size, batch_size, ...}`，图生图把参考图作为
  `image` 字段传 `data:image/png;base64,...`。
- 响应解析：优先 `data[0].b64_json`，其次 `data[0].url`（远程下载走 SSRF 校验）。
- 超时：渠道 `timeout_seconds`（默认 60），`max_retries` 默认 2。
- `mock` 渠道（`MOCK_PROVIDER_ENABLED=1`）用标准库绘制确定性 PNG，仅测试/开发夹具使用。
- LLM 渠道（`channel_type=llm`）用于小说场景分析，请求 `{model, messages, temperature}`，
  响应取 `choices[0].message.content`。

---

## 6. 媒体（`apps/mediahub`）

### 6.1 `GET /api/images`

查询 `page`、`page_size`、`status`、`keyword`。分页形态同 §0.5；item 见下。

### 6.2 `GET /api/images/{id}`

```json
{"id":6,"prompt":"...","width":1024,"height":1024,"file_size":8883,
 "status":"available","task_no":"...","created_at":"...","expires_at":"..."}
```

仅能访问自己的图片（租户隔离）；他人的图片返回 404 400404。

### 6.3 `POST /api/images/{id}/download-url`

```json
200 {"code":0,"message":"ok","data":{"url":"/downloads/<token>/","preview_url":"/downloads/<token>/?disposition=inline&preview=1"}}
```

### 6.4 `GET /downloads/{token}/`

查询参数：`disposition=inline|attachment`（默认 attachment）、`preview=1`。

- 普通下载 token：`TimestampSigner(salt=SECRET_KEY+":mediahub-download")`，有效期 **300 秒**；过期 403。
- 预览 token：稳定签名（不含时间戳），服务端校验 token 归属者与当前登录用户一致，否则 403/404。
- 响应头：`Content-Type`、`Content-Disposition`（文件名由提示词派生，`_download_filename`）、
  `Cache-Control`（预览长缓存，下载 no-store）。
- 预览快速路径优先返回预生成的 preview 文件。

### 6.5 分片上传协议

| 步骤 | 请求 | 说明 |
| --- | --- | --- |
| 1. 创建/续传 | `POST /api/uploads/sessions` `{kind, filename, total_size, total_chunks, client_upload_id?}` | `kind` ∈ `generation_reference_image` / `novel_epub`；命中 `client_upload_id` 时返回既有会话（续传） |
| 2. 查询 | `GET /api/uploads/sessions/{upload_id}` | 返回 `uploaded_chunks` 等 |
| 3. 传分片 | `PUT /api/uploads/sessions/{upload_id}/chunks/{index}` body = 原始二进制 | 索引从 0 开始；重复上传同一索引幂等覆盖 |
| 4. 完成 | `POST /api/uploads/sessions/{upload_id}/complete` | 校验齐全 → 合并 → 校验 sha256/size → `result_json` |
| 5. 取消 | `DELETE /api/uploads/sessions/{upload_id}` | 删除会话与分片 |

创建返回 **201**（实测），其余 200。会话状态 `uploading` / `completed` / `canceled` / `expired`。
`novel_epub` 完成后进入 EPUB 解析并创建小说可视化任务。
错误：400 400001（分片缺失/大小不符/类型不允许）、404 400404（会话不存在）、400 400003（状态不允许）。

### 6.6 软删除与保留

`GeneratedImage.status` ∈ `available` / `expired` / `deleted`。软删除置 `deleted` 并计入保留期；
保留期到期由清理任务把文件移入过期并删除对象。清理与在用图片之间的竞态由状态 + 保留期约束。

---

## 7. 小说可视化（`apps/novelviz`）

### 7.1 `GET /api/novelviz/books`

内置书目列表（`book_source="builtin"`）。

### 7.2 `POST /api/novelviz/books/upload`

`multipart/form-data`，字段 `file`（EPUB，≤ 50MB）。解析用 EPUB 容器（zip）+ HTML 抽取正文。

### 7.3 `POST /api/novelviz/tasks`

请求 `{book_id? | upload_id?, scene_mode, target_plot?}`。`scene_mode` ∈ `auto` / `targeted`。
最大场景数 `NOVELVIZ_MAX_SCENES`（默认 6），并发 `NOVELVIZ_SCENE_CONCURRENCY`（默认 4，钳制 1..8）。

```
200 {"code":0,"message":"ok","data":<serialize_novel_task>}
```

### 7.4 `GET /api/novelviz/tasks/{id}`

序列化字段：`{id,book_filename,book_source,uploaded_book_id,scene_mode,target_plot,status,progress_percent,error_message,scenes:[{number,title,chapter,story_significance,excerpt,visual_prompt}],logs:[...],images:[{id,scene_number,title,chapter,story_significance,excerpt,visual_prompt,status,attempts,error_message,image_url,preview_url,download_url,created_at,finished_at}],created_at,finished_at}`。

### 7.5 `POST /api/novelviz/tasks/{id}/retry`

重试失败任务的失败场景；400 400003 状态不允许时返回。

### 7.6 `DELETE /api/novelviz/tasks/{id}`

删除任务及其场景图。

### 7.7 `GET /api/novelviz/tasks/{id}/events`（SSE）

```
Content-Type: text/event-stream
Cache-Control: no-cache
X-Accel-Buffering: no
```

事件格式（逐字节）：

```
id: <递增整数>\nevent: <事件名>\ndata: <JSON>\n\n
```

心跳：每 15 秒 `: heartbeat\n\n`。轮询间隔 `NOVELVIZ_SSE_POLL_SECONDS`（默认 1）。
查询参数 `last_event_id` 指定从哪个事件之后开始补发（`last_event_id=999` 返回空流）。

事件名（实测）：`task_started`、`log`、`scene_plan_ready`、`scene_image_started`、
`scene_image_succeeded`、`scene_image_failed`、`task_succeeded`、`task_failed`。

`data` 为 JSON 对象，至少含 `progress_percent`；`log` 事件含 `message`。
事件日志上限 120 条（超出裁剪头部），`_fit_event_payload`/`_sanitize_event_payload` 保证载荷可序列化且不含机密。

### 7.8 `GET /api/novelviz/tasks/{id}/images/{image_id}.png`

查询 `preview=1`（优先返回预生成预览）、`download=1`（附件下载，带文件名）。仅任务归属者可访问。

---

## 8. 运营控制台（`apps/console`，前缀 `/console/api/`）

统一鉴权：未登录 401 401000；非运营 403 403000；状态不可用 403 403001；资源不存在 404 404000；
频率限制 429 429000（每账号 60 次/分钟，仅作用于 `/console/api/`）。
统一响应头：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Cache-Control: no-store`、
`Pragma: no-cache`、`X-Robots-Tag: noindex`。

| 端点 | 方法 | 说明 |
| --- | --- | --- |
| `/console/api/dashboard/` | GET | 概览：总账号数、总任务数、成功率、图片总数、最新任务、最新注册 |
| `/console/api/users/` | GET | 分页 `per_page`/`page`，筛选 `keyword`/`status`/`role` |
| `/console/api/users/{pk}/` | GET | 详情 |
| `/console/api/users/{pk}/quota/` | POST | 调整额度：`{daily_quota?, available_quota?}`；成功返回 `{user_id,daily_quota,available_quota,frozen_quota,total_generated_count,last_reset_date}`；错误 400001 缺少参数 / 400002 小于 0 |
| `/console/api/tasks/` | GET | 分页，筛选 `status`/`keyword` |
| `/console/api/config/` | GET | 配置列表：`{categories:[{code,label}],items:[{id,key,category,description,is_secret,value,updated_at}]}`；`?category=` 过滤。`is_secret` 项返回值一律掩码 |
| `/console/api/config/{key}/` | GET/PUT | 单项读写；GET 缺失键返回 200 空项（实测），PUT 缺失键 200 创建 |
| `/console/api/config/batch/` | PUT | 批量写 `{"items":[{"key","value"}]}` |
| `/console/api/config/` | PUT | **405**（未实现，须保持） |
| `/console/api/image-channels/` | GET/POST | 列表 / 创建（成功 **201**）；`channel_code` 唯一，重复 400 400002 `渠道编码已存在`；缺字段 400 400001 |
| `/console/api/image-channels/{pk}/` | GET/PUT/DELETE | 详情/更新/删除（删除 200） |

渠道序列化：`{id,name,channel_code,channel_type,provider_name,base_url,api_path,edit_api_path,model_name,timeout_seconds,max_retries,priority,is_active,created_at,updated_at}`。
**`api_key` 永不回显**（写入用 `api_key` 字段，读取时省略）。

---

## 9. LLM 对话（历史遗留接口，保留）

| 端点 | 方法 | 说明 |
| --- | --- | --- |
| `/api/chat/models` | GET | 返回 `channel_type=llm` 且在用的渠道 `[{channel_code,name,...}]` |
| `/api/chat` | POST | 请求 `{prompt, channel_code?, history?}`；成功 `{"code":0,"message":"ok","data":{"reply":"..."}}`；无可用模型 503 400503 |

`history` 元素 `{role: "user"|"model", parts:[{text}]}`；转发到上游时 `model` 角色映射为 `assistant`。
（该接口 SPA 已不再调用，但契约保留，见 `docs/review-0.0.1.md` P1-4。）

---

## 10. 审计（`apps/audit`）

无 HTTP 接口。两条记录链：

- `audit_adminoperationlog`：console 的写操作（额度调整、渠道增删改、配置读写）落库。
- `audit_callbackeventlog`：生成/小说任务的渠道回调事件落库。

---

## 11. 差分验证基线（对应 `tools/diff/`）

`main`（211 步）/ `email`（22 步）/ `upstream`（77 步）三条轨迹已在 Python 基线录制完成，
归一键：`csrftoken`、`sessionid`、验证码、时间戳、任务号、账本号、下载 token、图片哈希、
请求 id。上游录制（`upstream-final.jsonl`，13 条请求）记录方法、路径、请求头白名单、
请求体归一化摘要（multipart 记录各 part 的名称/文件名/内容类型/长度/宽高）与重试行为。

对比工具：`tools/diff/compare.py`（轨迹与快照的 unified diff）、`tools/diff/shots.mjs`（截图）。
截图基线落在 `docs/baseline/screenshots/`（12 张）。

---

## 12. 弃用与不移植（明确不在 0.0.1 契约内）

`POST /api/internal/release/publish`、`RELEASE_PUSH_*` 全部配置、`ALLOW_INSECURE_PROD`、
`CONSOLE_HMAC_ENABLED` 死路径、`AUTO_*` 启动开关、`GENERATION_TASKS_RUN_INLINE`、
`NOVELVIZ_RUN_INLINE`、`NOVELVIZ_USE_SUBPROCESS`、MinIO 双存储分支、腾讯云 SES、
`NOVELVIZ_*` 别名回退链、`django.contrib.admin`、`/pricing` 不存在但**必须继续 404**。
