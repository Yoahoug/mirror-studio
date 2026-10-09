# 绘镜 0.0.1 接口契约（冻结于 Django 1.x 基线）

本文件是 0.0.1 (Go) 重写必须逐字段保持的对外行为契约。

**来源**（全部为权威依据，无推测）：
`config/urls.py`、`apps/*/urls.py`、`apps/*/views.py`、`apps/*/forms.py`、`apps/*/serializers.py`、
`apps/*/services.py` 的序列化函数、两个 `frontend/*/src/services/apiService.ts`，
以及 `.reftmp/run-ref/` 中由 Python 基线真实录制并归一化的 HTTP 轨迹
（main 211 步 / email 22 步 / upstream 77 步）。轨迹中的实测值在本文中以「实测」标注。

---

## 0. 传输层与通用约定

### 0.1 凭据模式

| 项 | 值 |
| --- | --- |
| 会话 Cookie | `sessionid`；`HttpOnly`；`SameSite=Lax`；生产 `Secure` |
| CSRF Cookie | `csrftoken`；**非** `HttpOnly`；`SameSite=Lax`；生产 `Secure` |
| 凭据模式 | SPA 全部请求同源 + Cookie 会话；SSR 表单同源 Cookie |
| 写请求 CSRF 头 | `X-CSRFToken: <csrftoken>`，作用于 POST/PUT/PATCH/DELETE |
| 请求追踪头（响应） | `X-Request-Id`：回显请求 `X-Request-Id`，缺失时生成 16 位十六进制 |
| 通用响应头 | `X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`（全站，含 API） |

CSRF 失败：**403 + Django 风格 HTML 页面**（非 JSON 信封）。前端只按状态码处理。
登出成功：`Set-Cookie: sessionid=""; Max-Age=0; Path=/; SameSite=Lax`（实测）。

### 0.2 业务响应信封

成功 `{"code":0,"message":"ok","data":<payload>}`；失败 `{"code":<int>,"message":"<中文>","data":null}`。
`data` 允许为 `null`、对象、数组、`{}`。

**JSON 文本风格不是契约**：差分工具在解析后按 `sort_keys=True` 重新序列化比较，因此外层 JSON 的
键顺序与 `\uXXXX` 转义方式不参与比对。**但 `message` 字段内部的 JSON 字符串是逐字符比较的契约**
（见 §0.4），SSR HTML 也是逐字符比较的（见 §4）。

### 0.3 业务码表（全部来自代码与实测）

| code | HTTP | 含义 | 出现位置 |
| --- | --- | --- | --- |
| 0 | 200/201 | 成功 | 全局 |
| 400001 | 400 | 参数/表单/业务规则错误（默认码） | 全局 |
| 400002 | 400 | 唯一性冲突、额度数值非法 | console 渠道/额度 |
| 400003 | 400 | 状态不允许 | console 渠道删除 |
| 400100 | 400 | 上传/文件类参数错误 | 参考图、分片上传 |
| 400101 | 400 | 对象/文件缺失 | 生成任务删除、EPUB 上传 |
| 400102 | 400 | EPUB 解析失败 | 小说上传 |
| 400103 | 400 | 指定剧情过短 | 小说任务创建 |
| 400403 | 403 | 权限/风控拒绝 | 任务提交限流、注册开关、分片完成 |
| 400404 | 404 | 图片不可下载 | 下载链接签发 |
| 400429 | 429 | 登录锁定、验证码发送过频 | 登录、邮箱验证码 |
| 400500 | 500 | 小说任务创建/重试内部错误 | 小说 |
| 400501 | 501 → HTTP 500 | 未配置小说生图 Key | 小说任务创建 |
| 400503 | 503 | 无可用 LLM 渠道 | `/api/chat` |
| 401000 | 401 | 未登录 | console |
| 403000 | 403 | 非运营人员 | console |
| 403001 | 403 | 账号状态不可用 | console |
| 404000 | 404 | 资源不存在 | console |
| 429000 | 429 | console 频率限制（60 次/分钟/账号） | console |
| 500001 | 500 | 邮件后端未配置 | 邮箱验证码 |
| 500002 | 502 | 邮件发送失败 | 邮箱验证码 |

**注意**：部分端点的 404 来自 `get_object_or_404` / `Http404`，返回的是 **Django HTML 404 页面**，
不是 JSON 信封。这类端点在下方以「HTML 404」标注；差分工具对 ≥400 的 HTML 只记录状态码与是否含
「CSRF」字样，因此 Go 端只需返回 403/404 状态与 `text/html` 内容类型即可（不要求 HTML 内容一致）。

### 0.4 表单校验错误载荷

**`form.errors.as_json()` 的字符串**（`json.dumps`，`ensure_ascii=True`，分隔符 `", "` / `": "`，
字段顺序 = 声明顺序，非字段错误在 `__all__`）：

```
{"username": [{"message": "\u8d26\u53f7\u5df2\u5b58\u5728", "code": ""}], "email": [{"message": "\u90ae\u7bb1\u5df2\u5b58\u5728", "code": ""}]}
```

用于：`/api/auth/login`、`/api/auth/register`、`/api/auth/reset-password`。
前端 `readableError()`：`JSON.parse(message)` → 第一个字段 → `[0].message`。

**另有端点直接返回纯文本 message**（`_first_form_error`：取第一条错误的 `message`，兜底
`请求参数有误`）：`/api/auth/register/check`、`/api/auth/register/email-code`。
以及 `message` 为手写中文串的：`/api/account/settings`(PATCH)、`/api/account/password`。

必须复刻的文案（全部实测确认）：

| 场景 | message | code |
| --- | --- | --- |
| 必填缺失 | `这个字段是必填项。` | `required` |
| 邮箱格式 | `输入一个有效的 Email 地址。` | `invalid` |
| 超长 | `确保该变量包含不超过 150 字符 (目前字符数 160)。` | `max_length` |
| 密码过短 | `This password is too short. It must contain at least 8 characters.` | `password_too_short` |
| 密码过于常见 | `这个密码太常见了。` | `password_too_common` |
| 密码全数字 | `密码只包含数字。` | `password_entirely_numeric` |
| 密码与用户名相似 | `密码跟 用户名 太相似了。` | `password_too_similar` |

应用自定义（`code` 始终为 `""`）：`账号已存在`、`邮箱已存在`、`账号或密码错误`、
`账号状态不可用`、`验证码错误`、`两次密码输入不一致`、`用户不存在`、`邮箱不匹配`、
`至少提供账号或邮箱`、`邮箱验证码错误`、`验证码不存在或已过期`。

### 0.5 分页形态

只有两个业务端点分页，**默认 `page_size=24`，钳制 `1..48`**，`page` 下限 1：

```json
{"code":0,"message":"ok","data":{
  "items":[...],
  "pagination":{"page":1,"page_size":24,"total":37,"total_pages":2,
                "has_next":true,"has_previous":false}}}
```

适用于 `/api/generate/tasks`、`/api/workspace/history`。

**不分页的列表端点**（返回裸数组，实测）：
`/api/generate/topics`（上限 50，按 `-last_task_at`）、`/api/images`（上限 100）、
`/api/novelviz/books`、`/api/novelviz/tasks`（上限 30，按 `-created_at`）、
`/console/api/image-channels/`。

Console 分页端点（`/console/api/users/`、`/console/api/tasks/`）用 **`per_page`**，默认 20，钳制 `1..100`，
`pagination` 对象里是 `per_page`（不是 `page_size`），其余字段同名。

### 0.6 静态资源路径（前端零改动硬约束）

| 资源 | 路径 |
| --- | --- |
| 工作台入口 JS | `/static/workspace/assets/workspace.js` |
| 控制台入口 JS | `/static/console/assets/console.js` |
| 站点样式 | `/static/css/main.css` |
| 图标 | `/static/favicon.svg` |

模板中引用带 `?v={{ WORKSPACE_ASSET_VERSION }}` 查询串（值不参与契约，SPA 不读取）。

---

## 1. 页面路由

| 路径 | 方法 | 行为 |
| --- | --- | --- |
| `/` | GET | 302 → `/workspace` |
| `/workspace` | GET | 200 `workspace/index.html`；未登录且 `LOGIN_CAPTCHA_ENABLED` 时生成验证码写入 session 并注入上下文 |
| `/console`、`/console/` | GET | 未登录 302 → `/workspace?auth=login&next=/console/`；已登录但非运营 → **403 HTML** `当前账号没有管理员权限`；状态不可用 → 403 HTML `当前账号状态不可用` |
| `/admin`、`/admin/` | GET | 302 → `/console/` |
| `/healthz` | GET | 200 `{"status": "ok"}`（**受 IP 封禁中间件约束**，实测被封禁 IP 得到 403） |
| `/login` | GET | 200；已登录 302 → `/workspace` |
| `/register` | GET | 200；已登录 302 → `/workspace` |
| `/forgot-password` | GET | 200 |
| `/account` | GET | 200；未登录 302 → `/workspace?auth=login&next=/account` |
| `/account/security` | GET | 200；未登录 302 |
| `/account/history` | GET | 200；未登录 302 |
| `/account/history/<task_no>` | GET | 200；任务不存在 → HTML 404 |
| `/pricing` | GET | **404**（`HomeView`/`PricingView` 未路由；必须继续保持 404，见 review P1-2） |
| `/downloads/<token>/` | GET | 见 §6.4 |
| `/media/...` | GET | 仅 `DEBUG=1` 时由 Django 直供；生产无此路由 |

未登录访问受保护页：302 → `/workspace?auth=login&next=<原完整路径>`（`LOGIN_URL=/workspace?auth=login`）。
未登录访问受保护 API：同样 302 到 `/workspace?auth=login&next=/api/...`（实测）。
`LOGIN_REDIRECT_URL = /workspace`。

---

## 2. 认证与会话（`apps/users`）

所有端点响应 `application/json`；写操作需 CSRF。

### 2.1 `GET /api/auth/csrf` → 200
`{"code":0,"message":"ok","data":{"csrf_token":"<64字符>"}}`，并设置 `csrftoken` Cookie。

### 2.2 `GET /api/auth/captcha` → 200
`{"code":0,"message":"ok","data":{"captcha_code":"<4位>","captcha_svg":"data:image/svg+xml;base64,..."}}`。
`LOGIN_CAPTCHA_ENABLED=0` 时两者为空串。验证码写入 session，字母表
`23456789ABCDEFGHJKLMNPQRSTUVWXYZ`，长度 4，比较前 `upper()`，长度上限 8。

### 2.3 `POST /api/auth/login`

请求：`username`、`password`、`captcha_code`（`LOGIN_CAPTCHA_ENABLED=1` 时校验）。

```
200 {"code":0,"message":"ok","data":{"username":"admin"}}          ← 实测，注意没有 next
```

| 失败场景 | HTTP | code | message |
| --- | --- | --- | --- |
| 表单校验 | 400 | 400001 | `as_json()` 串，如 `{"captcha_code":[{"message":"验证码错误","code":""}]}` |
| 账号或密码错误 | 400 | 400001 | `{"__all__":[{"message":"账号或密码错误","code":""}]}` |
| 账号状态不可用 | 400 | 400001 | `{"__all__":[{"message":"账号状态不可用","code":""}]}` |
| 风控锁定 | 429 | 400429 | `登录失败次数过多，请 15 分钟后再试`（实测，单位是分钟） |

字段声明顺序：`captcha_code`、`username`、`password`（决定 `as_json()` 的键顺序）。
登录成功副作用：`last_login_at`、`last_login_ip`（受信 XFF 或 REMOTE_ADDR）、清空失败计数、轮换 session。
注意：登录**失败**（表单校验不通过）会调用 `record_login_failure`；但风控前置检查
（`ensure_login_allowed`）不通过时直接 429，不再累加。

### 2.4 `POST /api/auth/logout`
`Content-Type` 含 `application/json` → 200 `{"code":0,"message":"ok","data":{}}`；
其他 → **302 → `/`**（实测为 302）。两者都清空 session（幂等，无需登录）。

### 2.5 `POST /api/auth/register/check`

请求 `{"username"?, "email"?}`（至少一个）。字段均可选，但都为空 → 400001 `至少提供账号或邮箱`。

```
200 {"code":0,"message":"ok","data":{"username":{"available":true,"message":"账号可用"},
                                     "email":{"available":false,"message":"邮箱已存在"}}}
```
只回显传入的字段。`message` 取值：`账号可用`/`账号已存在`/`邮箱可用`/`邮箱已存在`。
表单校验失败 → 400 400001 + `_first_form_error` 纯文本。
`REGISTER_ENABLED=0` → 403 400403 `当前站点已关闭注册`。

### 2.6 `POST /api/auth/register/email-code`

请求 `{"username": ..., "email": ...}`（**两者都必填**，且都会做唯一性校验）。

```
200 {"code":0,"message":"验证码已发送，请留意邮箱","data":{"expire_seconds":600,"resend_seconds":60}}
```

| 失败 | HTTP | code | message（实测） |
| --- | --- | --- | --- |
| 表单校验 | 400 | 400001 | `账号已存在` / `这个字段是必填项。` / `邮箱已存在` / 邮箱格式文案 |
| 发送过频 | 429 | 400429 | `验证码发送过于频繁，请 60 秒后再试` |
| 邮件后端未配置 | 500 | 500001 | `邮件发送配置不完整：缺少 EMAIL_HOST, EMAIL_HOST_USER, EMAIL_HOST_PASSWORD` |
| 邮件发送失败 | 502 | 500002 | 上游错误信息 |
| 注册关闭 | 403 | 400403 | `当前站点已关闭注册` |

配置：码长 `email.verification_code_length`(6)、TTL `email.verification_ttl_seconds`(600)、
重发冷却 `email.verification_resend_seconds`(60)、每邮箱每日 `email.daily_limit_per_email`(10)、
每 IP 每日 `email.daily_limit_per_ip`(30)。验证码存储前做 `sha256` 摘要；不进入日志。

### 2.7 `POST /api/auth/register`

请求：`username`、`email`、`email_verification_code`（`email.verification_enabled=1` 时必填）、
`password`、`confirm_password`。

```
200 {"code":0,"message":"ok","data":{"username":"diff-user"}}      ← 实测，注册成功即自动登录
```

失败：400 400001 + `as_json()` 串，实测样例：
`{"username":[...账号已存在...],"email":[...邮箱已存在...]}`、
`{"__all__":[{"message":"两次密码输入不一致","code":""}]}`、
`{"__all__":[{"message":"这个密码太常见了。","code":"password_too_common"},{"message":"密码只包含数字。","code":"password_entirely_numeric"}]}`、
`{"__all__":[{"message":"邮箱验证码错误","code":""}]}`、
`{"__all__":[{"message":"验证码不存在或已过期","code":""}]}`。
校验顺序：字段级 → `clean()`（验证码 → 密码一致性 → 密码强度）。密码强度用 Django
`validate_password(password)`（**不传 user**，因此不会触发相似度校验）。
`REGISTER_ENABLED=0` → 403 400403 `当前站点已关闭注册`。

### 2.8 `POST /api/auth/reset-password`

请求：`captcha_code`、`username`、`email`（可选）、`new_password`、`confirm_password`。

```
200 {"code":0,"message":"ok","data":{}}
```
失败：400 400001 + `as_json()`（`用户不存在` / `邮箱不匹配` / `两次密码输入不一致` / 密码强度 / 验证码错误）。
成功写 `audit_callbackeventlog`（`event_type=password_reset`、`source=users`）。

### 2.9 `GET /api/account/summary`

```json
{"code":0,"message":"ok","data":{
  "user":{"username","nickname","email","avatar","remark"},
  "quota":{"daily_quota":20,"available_quota":20,"frozen_quota":0,
           "total_generated_count":0,"last_reset_date":"2026-10-09"},
  "recent_tasks":[{"task_no","status","created_at"}...]}}
```
`recent_tasks` 取**最近 3 条**（`-created_at`），且经过 `reconcile_recent_tasks` 运行时状态校正。

### 2.10 `GET /api/account/settings`

```json
{"code":0,"message":"ok","data":{
  "user":{"username","nickname","email","avatar","remark","role","status",
          "date_joined","last_login_at","last_login_ip","register_source"},
  "quota":{"daily_quota","available_quota","frozen_quota","total_generated_count","last_reset_date"}}}
```
`role`/`status` 输出**本地化显示值**（`普通用户`、`正常`…）。
`date_joined`/`last_login_at` 为 `isoformat()`（带微秒、无时区后缀）或 `null`；
`last_login_ip` 为空时是 `""`；`register_source` 空时回落 `website`。

### 2.11 `PATCH /api/account/settings`

可写：`nickname`（必填，截断 64）、`email`（可空，唯一性校验）、`avatar`（URL 校验）、`remark`（截断 500）。
成功 200 + 完整 §2.10 载荷，`message = "资料已更新"`。
失败 400 400001 + 纯文本：`昵称不能为空`、`邮箱已存在`、`头像地址格式不正确`。

### 2.12 `POST /api/account/password`

请求：`current_password`、`new_password`、`confirm_password`。

```
200 {"code":0,"message":"密码已更新","data":{}}
```
失败 400 400001 + 纯文本：`请输入当前密码`、`当前密码不正确`、`请输入新密码`、
`两次密码输入不一致`、密码强度首条文案（实测 `这个密码太常见了。`）。
成功时 `update_session_auth_hash`（保持登录）并写 `audit_callbackeventlog`（`event_type=password_change`）。

---

## 3. 风控（`apps/risk`）

无独立接口。参数读 `settings.RISK_*`（0.0.1 改为读 SiteConfig，见 review P2-1）。

| 机制 | 键/表 | 默认 | 结果 |
| --- | --- | --- | --- |
| IP 封禁 | `risk_ipaccessrule`（`is_effective` 且未过期） | 无 | `HttpResponseForbidden` → **403 text/html**，body `当前 IP 已被限制访问`；作用于**全部路径**（含 `/healthz`，实测） |
| 登录失败计数 | `risk:login:{username}:{ip}` | 窗口 900s，阈值 5 | 429 400429 `登录失败次数过多，请 15 分钟后再试` |
| 登录成功清零 | 同上 | — | 删除计数 |
| 任务提交限流 | `risk:task:{user_id}` | 窗口 60s | 403 **400403** `提交过于频繁，请稍后再试`（实测） |
| 敏感词 | `risk_sensitivewordrule`（`contains`/`exact`，`is_active`） | 无 | 400 400001 `提示词包含敏感词：{词}` |
| 客户端 IP | `TRUST_X_FORWARDED_FOR` | 生产 1 | 取 `X-Forwarded-For` 最左值；否则 `REMOTE_ADDR` |

登录失败计数与任务限流读写顺序（任务提交）：敏感词 → IP → 限流 → 额度。

---

## 4. 额度（`apps/quota`，原 `apps/billing`）

| 配置 | 默认 |
| --- | --- |
| `daily_quota`（每日免费额度） | 20 |
| 每日任务上限 | 20（与 `daily_quota` 同源） |
| pending 任务上限（queued+running） | 3 |
| 账本号 | `L%Y%m%d%H%M%S` + 8 位大写十六进制 |
| 变更类型 | `reset` / `freeze` / `consume` / `unfreeze` / `admin_adjust` / `daily_quota_update` |
| 方向 | `in` / `out` / `none` |

跨天重置按本机时区 `localdate()`；`refresh_daily_quota` 在每次读取额度前调用。
冻结-消费-释放：提交即冻结 1 并写 `freeze`；成功 `consume`；失败/取消 `unfreeze`。
任务 `quota_finalized` 保证只结算一次。

---

## 5. 生成（`apps/generator`）

### 5.1 主题

`GET /api/generate/topics` → 200，`data` 为**裸数组**（≤50 条，按 `-last_task_at`）：

```json
{"id":1,"title":"差分主题","cover_image_id":null,"last_task_at":"2026-10-09T20:54:53.268693+00:00"}
```
`POST /api/generate/topics` 请求 `{title}`（空则 `新的创作`，截断 128）→ 200 单个主题对象。

### 5.2 `POST /api/generate/reference-images`

请求 **JSON**：`{reference_image_data_url, reference_image_name}`（data URL 形式的图片）。
> 注意：不是 multipart。分片上传是另一套协议（§6.5）。

```
200 {"code":0,"message":"ok","data":{
      "reference_upload_token":"<签名串>",
      "reference_image_name":"fruit.png",
      "reference_image_attached":true}}
```
失败：400 400001（载荷解析）、400 400100（`参考图过大，请压缩后再试` 或 `请先上传参考图`）。
`token` 用 Django `TimestampSigner(salt="generator.reference-upload")` 生成。

### 5.3 `POST /api/generate/tasks`

请求字段（全部可选，有默认值）：
`prompt`、`negative_prompt`、`style_code`(general)、`size_code`(1024x1024)、`quality_code`(auto)、
`generation_mode`(text_to_image|image_to_image)、`reference_image_data_url`、`reference_image_name`、
`reference_upload_token`、`topic_id`、`client_request_id`、`wait_seconds`。

成功 **200**（不是 201）：

```json
{"code":0,"message":"ok","data":<serialize_task>}
```

`serialize_task` 字段与顺序（实测）：`task_no`、`status`、`progress_percent`、`generation_mode`、
`prompt`、`negative_prompt`、`style_code`、`size_code`、`quality_code`、`reference_image_name`、
`reference_image_attached`、`reference_image_data_url`（本端点恒为 `""`）、`quota_cost`、`error_code`、
`error_message`、`topic_id`、`submitted_at`、`started_at`、`finished_at`、`images`。
`images[i]` = `{id,status,width,height,expires_at}`，当 `include_urls` 且图片 `available` 时**追加**
`preview_url`、`download_url`。

失败：400 400001（载荷）、400 400100（参考图过大 / 尺寸或参数非法）、403 400403（限流）。
`wait_seconds` ∈ `[0,10]`：请求内轮询，`milestones = [35,52,68,82,90]`，
每 0.5s 刷新并按步进提升 `progress_percent`，直到终态或已产出图片。

尺寸规范化：边长对齐 16 的倍数、最大边 ≤2048、长宽比 ≤3:1、像素总数 ≥655360。

### 5.4 `GET /api/generate/tasks`

查询：`topic_id`、`q`（或 `keyword`，`prompt__icontains`）、`page`、`page_size`（默认 24，钳制 1..48）。
响应见 §0.5，`items` 为 `serialize_task(...)`（`include_urls=True`，且经 `reconcile_task_runtime_state`）。
`is_deleted=False` 过滤。

### 5.5 `GET /api/generate/tasks/{task_no}`

200 `serialize_task`（`include_urls=True`、`include_reference=False`）。
不存在 → **HTML 404**（`get_object_or_404`，实测 `/api/generate/tasks/NOSUCHTASK` → 404 HTML）。

### 5.6 `DELETE /api/generate/tasks/{task_no}`

历史软删除。成功 200 `{"code":0,"message":"ok","data":{"task_no":"...","deleted":true}}`；
不允许删除时 400 400101 + 文案。

### 5.7 `POST /api/generate/tasks/{task_no}/cancel`

```json
200 {"code":0,"message":"ok","data":{"task_no":"...","status":"canceled"}}
```
（只回任务号与状态，不是完整 task）。同时释放冻结额度。

### 5.8 `GET /api/workspace/history`

合并生成任务与小说任务，按 `created_at` 倒序（同一时刻用 `id` 兜底），分页形状见 §0.5
（默认 24，钳制 1..48）。生成侧条目为 `serialize_task`；小说侧为
`_serialize_novel_history_task`：

```json
{"task_no":"novelviz:1","task_kind":"novelviz","novel_task_id":1,"status":"succeeded",
 "progress_percent":100,"generation_mode":"novel_visualization",
 "prompt":"小说可视化：diff-fixture.epub","negative_prompt":"","style_code":"",
 "size_code":"1024x1024","quality_code":"auto","reference_image_name":"diff-fixture.epub",
 "reference_image_attached":false,"reference_image_data_url":"","quota_cost":0,
 "error_code":"","error_message":"","topic_id":null,
 "book_filename":"...","scene_mode":"auto","target_plot":"",
 "submitted_at":"...","started_at":"...","finished_at":"...",
 "images":[{"id","status","width":1024,"height":1024,"expires_at":null,
            "preview_url","download_url","image_url","scene_number","title",
            "chapter","story_significance","excerpt"}]}
```
`prompt` 在 `targeted` 模式下是 `小说指定剧情：{book_filename}`，否则 `小说可视化：{book_filename}`。

### 5.9 任务状态机

状态 `queued` → `running` → `succeeded` / `failed` / `canceled`。
- 认领：`running, lease_expires_at = now + max(stalled_timeout, 180)`，`execution_attempts += 1`，
  `progress_percent = max(旧值, 25)`；`started_at` 只写一次；`finished_at` 清空。
  认领条件：`queued`，或 `running` 且 `lease_expires_at IS NULL`，或 `running` 且 `lease_expires_at <= now`。
  取 `submitted_at, id` 最早的（**仅队列扫描时**）。
- 重试：`_is_retryable_error` = HTTP 状态为 None、≥500、或 ∈ {408,409,425,429}；
  次数上限 `channel.max_retries`；重试前 `sleep(min(1.5*attempt, 3))` 秒。
- 终态失败：写 `error_code`/`error_message`，`error_message` 对外经
  `build_public_error_message` 归一为统一公开文案；释放冻结额度。
- 卡死回收：`reconcile_task_runtime_state` / `requeue_task_after_worker_loss` 把租约过期的
  `running` 任务退回 `queued`，`progress_percent = clamp(旧值, 8, 20)`，清空租约。

### 5.10 渠道分发与外部协议

`provider_vendor` ∈ `openai` / `siliconflow`。

- **openai 文生图**：`POST {base_url}{api_path}`，JSON
  `{model, prompt, size, quality, n, response_format:"b64_json", ...}`；实际字段以
  `tools/diff/fake_upstream.py` 录制的 13 条请求为准（S5 逐字段比对）。
- **openai 图生图**：优先 `multipart/form-data` 打到 `edit_api_path`（未配置则 `api_path`），
  part：`image`（文件）、`prompt`、`size`、`model`；上游返回 400/404/405/415/422 时降级为
  JSON `{"image_url": "data:image/png;base64,..."}` 重试一次。
- **siliconflow**：JSON `{model, prompt, image_size, batch_size, ...}`；图生图把参考图放入
  `image` 字段（data URI）。
- 响应解析：优先 `data[0].b64_json`，否则 `data[0].url`（远程下载）。
- LLM 渠道（`channel_type=llm`）：`POST {base_url}{api_path}` `{model, messages, temperature}`，
  取 `choices[0].message.content`。
- 超时：渠道 `timeout_seconds`（默认 60）；重试：`max_retries`（默认 3）。
- `mock` 渠道（`MOCK_PROVIDER_ENABLED=1`）仅测试/开发夹具使用。

---

## 6. 媒体（`apps/mediahub`）

### 6.1 `GET /api/images`
查询 `status`、`keyword`（`task__prompt__icontains`）。`data` 为**裸数组**，**不分页**，上限 100。
过滤 `task__is_deleted=False`。item：

```json
{"id":6,"task_no":"...","prompt":"...","status":"available",
 "width":1024,"height":1024,"file_size":8883,
 "created_at":"...","expires_at":"..."}
```

### 6.2 `GET /api/images/{id}` → 200 同 item；不存在或非本人 → **HTML 404**。

### 6.3 `POST /api/images/{id}/download-url`

```json
{"code":0,"message":"ok","data":{"url":"/downloads/<token>/",
                                 "preview_url":"/downloads/<token>/?disposition=inline&preview=1"}}
```
图片状态非 `available` → 404 400404 `图片已过期或不可下载`。不存在/非本人 → HTML 404。

### 6.4 `GET /downloads/{token}/`

查询：`disposition=inline|attachment`（默认 attachment）、`preview=1`。
- 普通 token：`TimestampSigner(salt=<派生自 SECRET_KEY + ":mediahub-download">)`，**max_age 300 秒**；过期或无效 → **403 HTML**。
- 预览 token：稳定签名（无时间戳），但服务端校验 token 归属者 = 当前登录用户，否则 403。
- 响应头：`Content-Type`、`Content-Disposition`（文件名由提示词派生：`\s+`→空格、
  去掉 `\/:*?"<>|`、截断 48 字符，空则用 `task_no`；扩展名由 mime 推断，默认 `.png`）、
  `Cache-Control`（预览长缓存 / 下载 no-store）。
- 预览优先生成/复用预生成缩略图（最大边 768）。

### 6.5 分片上传协议

| 步骤 | 请求 | 成功 |
| --- | --- | --- |
| 1 创建/续传 | `POST /api/uploads/sessions` `{kind, filename, content_type, total_size, client_upload_id?}` | **201** + 会话对象 |
| 2 查询 | `GET /api/uploads/sessions/{upload_id}` | 200 + 会话对象 |
| 3 传分片 | `PUT /api/uploads/sessions/{upload_id}/chunks/{index}`，body = 原始二进制 | 200 + 会话对象 |
| 4 完成 | `POST /api/uploads/sessions/{upload_id}/complete` | 200 + 结果 |
| 5 取消 | `DELETE /api/uploads/sessions/{upload_id}` | 200 + 取消后的会话对象 |

`kind` ∈ `generation_reference_image` / `novel_epub`。分片索引从 0 开始，末片长度 =
`total_size - chunk_size*(total_chunks-1)`；`chunk_size = RESUMABLE_UPLOAD_CHUNK_SIZE`（默认 8388608）。
会话对象：`{upload_id, kind, filename, status, chunk_size, total_chunks, uploaded_chunks[],
uploaded_size, total_size, expires_at, result?}`。
会话状态 `uploading` / `completed` / `canceled` / `expired`。
错误：400 400001（载荷）、400 400100（分片长度不符/类型非法/状态非法）、
403 400403（归属或状态冲突）、**HTML 404**（会话不存在）。`client_upload_id` 命中则返回既有会话（续传）。

### 6.6 软删除与保留
`status` ∈ `available` / `expired` / `deleted`。软删除置 `deleted` 并记录 `deleted_at`；
保留期到期由清理任务转 `expired` 并删对象。清理只处理到期的 `deleted` 行，避免与在用图片竞态。

---

## 7. 小说可视化（`apps/novelviz`）

### 7.1 `GET /api/novelviz/books` → 裸数组
项：`{id, source, filename, display_name, size, uploaded_book_id, created_at}`。

### 7.2 `POST /api/novelviz/books/upload`
**multipart/form-data**，字段 `file`（EPUB）。成功 **201** + 上传书对象。
缺少文件 → 400 400101 `请选择 EPUB 文件`；解析失败 → 400 400102 + 原因。

### 7.3 `GET /api/novelviz/tasks` → 裸数组（≤30，`-created_at`）
读取前对每条调用 `_rescue_stalled_task`（异常被忽略）。`serialize_task(include_logs=False)`。

### 7.4 `POST /api/novelviz/tasks`
请求：`book_source`(builtin|uploaded)、`book_filename`、`uploaded_book_id`、
`llm_channel_code`、`scene_mode`(auto|targeted)、`target_plot`。

成功 **201** + `serialize_task`。失败：
400 400103（`请填写想呈现的剧情`，targeted 且 `len(target_plot) < 4`）、
400 400001（载荷）、400 400100（书籍路径/LMM 渠道解析）、
500 400500（内部错误）、500 400501（`未配置小说生图模型 Key`）。
`scene_mode` 非法值静默回落 `auto`；`target_plot` 截断 2000，auto 模式强制清空。

### 7.5 `GET /api/novelviz/tasks/{id}` → 200 `serialize_task()`（含 logs）；不存在 → HTML 404。
### 7.6 `DELETE /api/novelviz/tasks/{id}` → 200 `{"task_id":N,"deleted":true}`；不存在 → HTML 404。
### 7.7 `POST /api/novelviz/tasks/{id}/retry`

成功 200 + `serialize_task`（重置失败场景后重新派发）；
不存在 → **HTML 404**；`NovelVisualizationError`/`ValueError` → 400 400100；其他 → 500 400500。

### 7.8 `GET /api/novelviz/tasks/{id}/events`（SSE）

响应头：`Content-Type: text/event-stream`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`。
任务不存在 → HTML 404。

事件序列化（逐字节）：

```
id: <事件 id>\nevent: <事件类型>\ndata: <JSON, ensure_ascii=False>\n\n
```

事件来自任务的 `logs` 数组元素 `{id, type, payload}`；`type` 即事件名。
查询参数 `last_event_id`（默认 0）只推送 id 更大的事件。实测事件名：
`task_started`、`log`、`scene_plan_ready`、`scene_image_started`、`scene_image_succeeded`、
`scene_image_failed`、`task_succeeded`、`task_failed`。
心跳：空闲 ≥15s 时推送 `: heartbeat\n\n`。轮询间隔 `NOVELVIZ_SSE_POLL_SECONDS`（下限 0.25s）。
终止条件：任务处于终态**且**已推送完 `last_event_id`；
任务被删除时推送 `event: task_failed\ndata: {"message":"任务不存在"}\n\n` 后结束。
`logs` 上限 120 条（超出裁剪最旧），载荷经 `_sanitize_event_payload` 保证可序列化且不含机密
（`logs` 键被清空、dict 键截断 120、list 截断 50）。

### 7.9 `GET /api/novelviz/tasks/{id}/images/{image_id}.png`

查询 `preview=1`、`download=1`、`disposition=inline`。
`inline = not download or disposition==inline or preview`。
`Content-Disposition: {inline|attachment}; filename="novel-scene.png"; filename*=UTF-8''<percent-encoded>`
（文件名 `_scene_filename`：标题去 `\/:*?"<>|`、截断 48，空则 `scene-NN.png`）。
`Cache-Control`：预览命中预生成缩略图 → `private, max-age=86400, immutable`；
其他 → `private, max-age=3600`。
路径安全：`media_root` 必须是解析后路径的祖先，否则 HTML 404。

---

## 8. 运营控制台（`/console/api/`）

鉴权：未登录 401 401000 `未登录`；状态不可用 403 403001 `当前账号状态不可用`；
非运营 403 403000 `权限不足`；载荷解析失败 400 400001。
频率限制 429 429000（每账号 60 次/分钟）。
响应头（**仅 `/console/api/` 路径**）：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、
`Cache-Control: no-store`、`Pragma: no-cache`、`X-Robots-Tag: noindex`。

| 端点 | 方法 | 响应 |
| --- | --- | --- |
| `/console/api/dashboard/` | GET | `{today:{new_users,tasks,failed_tasks,succeeded_tasks}, totals:{users,images,tasks,active_channels}, latest_tasks[10], latest_users[10], recent_failures[10]}` |
| `/console/api/users/` | GET | `{items[],pagination}`；筛选 `keyword`(username/email/mobile icontains)、`status`、`role`；`per_page` 默认 20 |
| `/console/api/users/{pk}/` | GET | 用户详情 + `profile`；404 404000 `用户不存在` |
| `/console/api/users/{pk}/quota/` | POST | `{user_id,daily_quota,available_quota,frozen_quota,total_generated_count,last_reset_date}` |
| `/console/api/tasks/` | GET | `{items[],pagination}`；筛选 `keyword`(task_no/username/prompt)、`status` |
| `/console/api/config/` | GET | `{items[],categories[]}`；`?category=` 过滤；`is_secret` 项的值一律掩码 |
| `/console/api/config/{key}/` | GET/PUT | GET 缺失 → 404 404000 `配置项不存在`；PUT 缺失 → 创建，返回 `{key,updated_at}`；PUT 无 `value` → 400 400001 `缺少 value` |
| `/console/api/config/batch/` | PUT | `{"items":[...]}` → `{"updated":N}`；无 items → 400 400001 `缺少 items 数组` |
| `/console/api/config/` | PUT | **405**（未实现，须保持） |
| `/console/api/image-channels/` | GET/POST | POST 必填 `name/channel_code/base_url/model_name`，缺 → 400 400001 `缺少 {field}`；重复 → 400 400002 `渠道编码已存在`；`channel_type` 非法 → 400 400001 `不支持的模型类型`；成功 **201** |
| `/console/api/image-channels/{pk}/` | GET/PUT/DELETE | 404 404000 `渠道不存在`；DELETE 有关联任务 → 400 400003 `该渠道有关联任务，无法删除，请先禁用`；成功 `{"deleted":true}` |

用户列表项：`{id,username,email,mobile,role,status,is_staff,date_joined,last_login,last_login_ip,quota:{daily_quota,available_quota,frozen_quota,total_generated_count,last_reset_date}|null}`；
详情额外含 `profile:{nickname,avatar,register_ip,register_source,remark}|null`。

任务列表项：`{id,task_no,user_id,username,status,prompt,style_code,size_code,quality_code,channel_name,quota_cost,error_code,error_message,provider_request_id,provider_response_json,created_at,finished_at}`。

失败日志项：`{id,event_type,source,success:false,remark,task_no,error_code,error_message,provider_request_id,payload_json,created_at}`。

渠道项：`{id,name,channel_code,channel_type,provider_name,base_url,api_path,edit_api_path,model_name,timeout_seconds,max_retries,is_active,priority,created_at,updated_at}`。
**`api_key` 永不回显**（写入用 `api_key` 字段）。

配置项：`{id,key,value,is_secret,description,category,updated_at}`；分类 `general/site/email/risk`
（`通用/站点/邮件验证/风控`）。
额度调整校验：两者皆缺 → 400 400001 `至少传入 daily_quota 或 available_quota`；
`available_quota < 0` → 400 400002 `当前剩余次数不能小于 0`；
`daily_quota < 0` → 400 400002 `每日免费次数不能小于 0`。
调整备注写为 `[管理员:{username}] {remark 或 后台修改免费次数}`。

---

## 9. LLM 对话（保留的历史接口）

| 端点 | 方法 | 说明 |
| --- | --- | --- |
| `/api/chat/models` | GET | 裸数组，仅 `channel_type=llm` 且在用的渠道 |
| `/api/chat` | POST | 请求 `{prompt, channel_code?, history?}`；成功 `{"code":0,"message":"ok","data":{"reply":"..."}}`；无可用渠道 → 503 400503 |

`history` 元素 `{role:"user"|"model", parts:[{text}]}`；转发上游时 `model` → `assistant`。

---

## 10. 审计（`apps/audit`）

无 HTTP 接口。
- `audit_adminoperationlog`：console 写操作（额度调整、渠道增删改、配置写）。
- `audit_callbackeventlog`：任务/用户侧事件（`password_change`、`password_reset`、`generation_failed` 等）。

---

## 11. 环境变量契约（差分冻结集，`tools/diff/run.py:COMMON_ENV`）

差分运行时两个后端共用同一组环境变量，因此下列变量的**语义与默认值**是契约的一部分：
`SECRET_KEY`、`DATABASE_URL`、`ALLOWED_HOSTS`、`PUBLIC_ORIGIN`、`DEBUG`、
`REGISTER_ENABLED`、`LOGIN_CAPTCHA_ENABLED`、`EMAIL_VERIFICATION_ENABLED`、
`EMAIL_VERIFICATION_CODE_LENGTH`、`EMAIL_VERIFICATION_TTL_SECONDS`、
`EMAIL_VERIFICATION_RESEND_SECONDS`、`EMAIL_DAILY_LIMIT_PER_EMAIL`、`EMAIL_DAILY_LIMIT_PER_IP`、
`EMAIL_HOST`/`EMAIL_PORT`/`EMAIL_HOST_USER`/`EMAIL_HOST_PASSWORD`/`EMAIL_FROM`/`EMAIL_USE_TLS`、
`RISK_LOGIN_FAIL_LIMIT`、`RISK_LOGIN_FAIL_WINDOW_SECONDS`、
`RISK_TASK_LIMIT_PER_WINDOW`、`RISK_TASK_WINDOW_SECONDS`、
`DAILY_FREE_QUOTA`、`MAX_PENDING_TASKS`、
`MOCK_PROVIDER_ENABLED`、`REMOTE_IMAGE_ALLOWED_HOSTS`、`MAX_REMOTE_IMAGE_BYTES`、
`MAX_REFERENCE_IMAGE_BYTES`、`MAX_NOVEL_EPUB_BYTES`、`RESUMABLE_UPLOAD_CHUNK_SIZE`、
`NOVELVIZ_MAX_SCENES`、`NOVELVIZ_SCENE_CONCURRENCY`、`NOVELVIZ_SSE_POLL_SECONDS`、
`NOVELVIZ_IMAGE_API_KEY`、`NOVELVIZ_IMAGE_BASE_URL`、`NOVELVIZ_IMAGE_MODEL`、
`IMAGE_PREVIEW_MAX_DIMENSION`、`TRUST_X_FORWARDED_FOR`、`USE_X_FORWARDED_HOST`、
`FIELD_CRYPT_KEY`、`WORKSPACE_VITE_DEV_URL`、`CONSOLE_VITE_DEV_URL`、
`WORKSPACE_RELEASE_VERSION/TITLE/NOTES/PUBLISHED_AT`、`GENERATION_WORKER_CONCURRENCY`、
`GENERATION_WORKER_POLL_SECONDS`。

---

## 12. 明确不在 0.0.1 契约内（不移植）

`POST /api/internal/release/publish`、全部 `RELEASE_PUSH_*`、`ALLOW_INSECURE_PROD`、
`CONSOLE_HMAC_ENABLED` 与 console 的 HMAC 死路径、`AUTO_APPLY_MIGRATIONS`/`AUTO_SEED_SITECONFIG`/
`AUTO_INIT_APP_DATA`/`AUTO_COLLECTSTATIC`、`GENERATION_TASKS_RUN_INLINE`、`NOVELVIZ_RUN_INLINE`、
`NOVELVIZ_USE_SUBPROCESS`、MinIO 双存储分支与 `MINIO_*`、腾讯云 SES 与 `TENCENT_SES_*`、
`NOVELVIZ_*` 别名回退链（保留语义独立的那几个）、`django.contrib.admin`、
`templates/billing/payment_checkout.html`、`templates/account/orders.html`、
`templates/account/quota.html`、`templates/console/dashboard.html`、
`templates/public/home.html`、`templates/public/pricing.html`、`templates/partials/package_cards.html`、
`scripts/make_update_zip.py`、`scripts/export_deploy_bundle.py`、`docker/nginx/`、
`docker/start-beat.sh`、`docker/start-worker.sh`、`scripts/wait_for_db.py`、
`apps/billing` 的空 `urls.py`/`views.py`/`tasks.py`/`gateways/` 占位文件、
两个 SPA 中的 `geminiService.ts` 死桩。

`/pricing` 返回 404 属于当前线上事实，**必须继续返回 404**。
