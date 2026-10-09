# 0.0.1 安全清单逐项核对（S5 审计结果与修复台账）

审计时间：2026-10-09 / 10。方法：对第二节第 9 节的 12 项安全清单，逐项对照
`apps/`（Django 参考实现）与 `internal/` + `main.go`（Go 重写）的**实际取值**
（阈值、时长、默认值、header 名、cookie 属性、状态码、错误文案），不看注释与命名。

本文件是**待办台账**：每条给出「参考实现位置 / Go 位置 / 结论」。标记为
`待修复` 的条目必须修完并在 `docs/verification-0.0.1.md` 中给出复核结果；
`接受` 的条目必须在最终报告中单列理由。

> 与 `docs/review-0.0.1.md` 的差异：审查清单针对 **Django 侧**的问题与移植决策，
> 本文件针对 **Go 侧与参考实现的行为/安全等价性**，两者互补。审查清单 P1-32 称
> `CONSOLE_HMAC_ENABLED` "从未在 settings 中定义"，实际 `config/settings/base.py:129`
> 有定义（默认 False），该条描述需更正（结论不变：开关本身不移植）。

---

## 汇总

| # | 项目 | 结论 | 待修复项数 |
| --- | --- | --- | --- |
| 1 | 密码哈希 Argon2 参数不降低 | 参数一致；**遗留哈希不回写** | 1 |
| 2 | 生产无条件校验、无跳过开关 | Go 更严（无 `ALLOW_INSECURE_PROD`）；**缺 MOCK 校验** | 1 |
| 3 | CSRF / 会话 cookie / 安全响应头 / HSTS | 属性一致；**缺会话 auth-hash、CSRF 轮换、Origin 校验、HTTPS 重定向、静态头** | 5 |
| 4 | 登录验证码与邮箱验证码语义 | 码长/字符集/TTL/文案一致；**验证码消费顺序不同**；**SSR 验证码用 math/rand** | 2 |
| 5 | 风控阈值不变 | 默认值一致；**SSR 登录绕过锁定**；**风险事件不落库** | 2 |
| 6 | 控制台校验不变、死 HMAC 路径不移植 | 权限与失败响应逐项一致；HMAC 确认未移植 | 0 |
| 7 | 上传安全不变 | 分片协议/限额/越权/路径逃逸一致；**参考图仅校验文件头**、**缺少 total_size 文案不同**、**EPUB 上传目录未读配置** | 3 |
| 8 | SSRF 防护不变 | 主机白名单/重定向/大小/MIME 一致；**IPv6 网段覆盖不全**、**重试分类不同**、**大写 scheme 被拒** | 3 |
| 9 | 字段加密不变、密钥不进日志 | 日志无泄漏（逐条核对）；**44 字节密钥二次派生（已修）**、**控制台掩码格式不同（已修）** | 0（已修） |
| 10 | 配额与限额默认值不变 | 默认值/冻结-扣减-退回语义一致；**取消/删除的台账备注不同**、**配额结算与终态写入非同一事务** | 2 |
| 11 | 任务一致性不变 | 状态机、认领、租约、重试、取消逐项一致；**配额原子性（同 10）** | 0（见 10） |
| 12 | 租户隔离不变 | 所有请求路径的读/下载/删除均带 owner 谓词；**无越权可达路径** | 0 |

---

## 1. 密码哈希

- 参考：`config/settings/base.py:270-273`（`Argon2PasswordHasher` + `PBKDF2PasswordHasher`，均为 Django 内置）
- Go：`internal/auth/auth.go:207-214`（参数）、`:219-228`（写入）、`:235-307`（校验与 pbkdf2 回退）

参数逐项一致：`argon2id` / `v=19` / `m=102400` / `t=2` / `p=8` / 摘要 32 字节。
盐：Go 为 20 字节 `crypto/rand` 后 base64（≈160 bit，与 argon2-cffi 输出同形）；
Django 为 22 字符 `[A-Za-z0-9]`（≈131 bit）。**Go 更强**，非降级。

- **待修复 P1**：`internal/auth/auth.go:243-245` 计算了 `needsRehash` 但
  `Authenticate`（`:526`）丢弃它，登录成功后**不回写**。参考实现
  `AbstractBaseUser.check_password` 会经 `verify_password` 的 `must_update`
  把 pbkdf2_sha256 哈希升级为 argon2（Django `hashers.py:54-60`）。
  后果：老库里的 pbkdf2 哈希永不升级。另：`auth.go:230-234` 的注释
  「参考实现的登录路径从不 rehash」与事实不符，需一并更正。
- 说明：Go 写出的哈希 Django 侧会因盐格式差异再次 rehash，不影响安全性。

## 2. 生产无条件校验

- 参考：`config/settings/prod.py:5-25`
- Go：`internal/config/config.go:204-462`（唯一校验入口）、`main.go:46-51`（失败即 `os.Exit(1)` 并打印原因）

Go 校验：`SECRET_KEY`（非空、非 `replace-me`、≥32 字符）、`FIELD_CRYPT_KEY`
（非空、≥8 字符）、`PUBLIC_ORIGIN`/`SITE_DOMAIN`（非 DEBUG 时必须是绝对 http(s) 源）、
`DATABASE_URL`（非空且仅 sqlite）。**无任何跳过开关**（`ALLOW_INSECURE_PROD`
未移植，符合第 7 节）。`FIELD_CRYPT_KEY` 为空时直接拒绝启动——参考实现的
「空密钥明文存储」缺陷正确地未被移植（`apps/core/security.py:11-13` 的行为已移除）。

- **待修复 P0**：`MOCK_PROVIDER_ENABLED` 在参考实现的生产配置里默认 False 且
  **开启即拒绝启动**（`config/settings/prod.py:11,24-25`），而 Go 默认 **True**
  （`internal/config/config.go:358`）且无生产校验。后果：生产环境按默认值启动会
  静默返回 mock 图片。修法：默认值改为 false；生产（DEBUG=0）下为真时启动失败
  并打印原因。差分语料显式传入该变量，故不受影响。
- 说明：`DEBUG=true` 会跳过 `PUBLIC_ORIGIN` 校验并关闭 Secure cookie/HSTS。
  这是既有的 dev/prod 分界，与参考实现的 `config.settings.dev` 对应，保留。

## 3. CSRF / 会话 / 响应头 / HSTS

- Go：`internal/httpapi/session.go:20-28`（名字与时长）、`:302-324`（校验）、
  `middleware.go:266-292`（安全头与 HSTS）、`:322-357`（cookie 写入与清除）、`:184-198`（校验与静态旁路）
- 参考：`config/settings/base.py:252-255`、`prod.py:12-17`

逐项一致：`sessionid`（Max-Age 1209600 / Path / / SameSite=Lax / HttpOnly / 生产 Secure）、
`csrftoken`（Max-Age 31449600 / Path / / Lax / **非** HttpOnly / 生产 Secure）、
登出清 cookie、`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、
`Referrer-Policy: same-origin`、`Cross-Origin-Opener-Policy: same-origin`、
`Strict-Transport-Security: max-age=31536000; includeSubDomains`（preload 关）。
CSRF 令牌语义（64 字符掩码 / 32 字符密钥 / 常量时间比较 / 403 含 CSRF 字样）一致。

- **待修复 P1（会话 auth-hash）**：Django 登录时写入 `_auth_user_hash`，认证中间件
  在哈希不匹配时清空会话（`django/contrib/auth/__init__.py:193,295-330`），
  因此改密/重置密码会失效其它会话。Go 会话只存 `user_id`
  （`internal/auth/auth.go:620-623`），改密只轮换当前会话（`handlers.go:459-462`）。
  后果：被盗的会话 cookie 在改密后仍有效（最长 14 天）。修法：会话内保存
  `sha256(password_hash)` 派生值，每请求比对，不匹配即 flush。
- **待修复 P1（CSRF 轮换）**：Django `login()` 调用 `rotate_token`；Go 登录只轮换
  会话键（`auth.go:616-629`）。修法：登录成功时轮换 CSRF 密钥并下发新 cookie。
- **待修复 P1（HTTPS 重定向）**：参考实现生产有 `SECURE_SSL_REDIRECT=True`
  （`prod.py:14`），Go 未实现该语义，也不读 `X-Forwarded-Proto`
  （`internal/config/config.go:254` 解析了 `USE_X_FORWARDED_HOST` 但未被使用）。
  修法：非 DEBUG 且未走 HTTPS（结合 `X-Forwarded-Proto`）时 301 到 https 源，
  与代理契约一起实现。
- **待修复 P2（CSRF Origin 校验）**：Django 对 HTTPS 请求比对 Origin 与
  `CSRF_TRUSTED_ORIGINS`（`django/middleware/csrf.py:425-443`），Go 只比对
  cookie 与令牌。SameSite=Lax 两边都在，属纵深防御项。
- **待修复 P2（静态响应头）**：Go 的 `/static/` 响应不带上述安全头
  （`middleware.go:191-198,268-270`）。DEV 下参考实现的静态由 runserver 的
  静态处理器直供、同样不带（差分语料已验证一致），但**生产**参考实现经
  WhiteNoise + SecurityMiddleware 会带上 nosniff/Referrer-Policy/COOP。
  修法：仅在非 DEBUG 时给静态响应补头，保持 DEV 逐字节一致。

## 4. 验证码与邮箱验证码

- 参考：`apps/users/utils.py:7-11`、`apps/users/forms.py:29-41`、
  `apps/users/email_verification.py:33-35,78-79,119-148,231-277`
- Go：`internal/auth/captcha.go:13-34`、`internal/auth/email_verification.go:41-45,209-324`

一致：验证码字母表 32 字符（去 0/1/I/O）、长度 4、大小写不敏感、
会话作用域无独立 TTL、非一次性（与参考实现相同）；邮箱码 6 位数字、
SHA-256 存储、TTL 600s、重发 60s、每邮箱 10 次/天、每 IP 30 次/天、
主题与正文、全部中文文案逐字一致。Go 用 `crypto/rand`（参考用 `random`），更强。

- **待修复 P1（消费顺序）**：Go 先删除再比对（`email_verification.go:89-101`
  的 `takeCode` 与 `:273`），一次输错就烧掉仍有效的验证码，下一次报
  `验证码不存在或已过期`；参考实现先校验、成功后才删除（`:275-277`）。
  用户可见行为不同，须改为参考实现的顺序。
- **待修复 P2（SSR 验证码）**：`internal/web/pages.go:92-100` 仍用
  `math/rand` + 时间种子，且 `internal/web/handlers.go:527` 直读
  `cfg.LoginCaptchaEnabled` 而不走 `site.login_captcha_enabled` 的 DB 覆盖
  （API 路径走的是覆盖，`handlers.go:967`）。修法：复用 `auth` 的 crypto 生成器
  与站点配置解析。

## 5. 风控阈值

- 参考：`config/settings/base.py:125-128`、`apps/risk/services.py:30-82`
- Go：`internal/config/config.go:345-354`、`internal/risk/service.go:114-266`

默认值逐项一致：登录失败 5 次、锁 15 分钟、任务窗口 60 秒、窗口内 10 次；
IP 规则、敏感词（精确/包含）、任务限流键与文案一致。Go 的
`is_blocked != 1` / 过期时间不可解析即拦截比参考更严。

- **待修复 P0（SSR 登录绕过锁定）**：参考的 `LoginPageView` POST 会调用
  `ensure_login_allowed` 并记录失败（`apps/users/views.py:94,108`）；
  Go 的 `internal/web/handlers.go:421-459` 只调 `Authenticate` + `signIn`，
  整个 `internal/web` 包不引用 risk。表单无 action，提交到 `/login`。
  后果：`POST /login` 可无限试密码、且不计数；已通过 API 锁定的账号仍能经该路径登录。
  （IP 级拦截仍在中间件生效。）修法：SSR 登录提交走与 API 相同的风控前置与失败记录。
- **待修复 P1（风险事件不落库）**：参考在命中敏感词与任务限流时写
  `risk_riskevent`（`apps/risk/services.py:63-68,76-81`），Go 只写
  `login_failure`（`internal/risk/service.go:161-168`，`insertRiskEvent` 仅一个调用点）。
  后果：控制台/审计看不到 prompt 拦截与限流记录。
- **接受（P2，限性能权衡）**：IP 规则查询缓存 60s（`service.go:26,98-112`）、
  敏感词缓存 30s（`:27,209-240`）；新增/解除拦截最长滞后一个窗口。
- **待修复 P2（DB 错误时放行）**：IP 规则查询出错时 Go 返回「未拦截」
  （`service.go:82-86`）；参考实现会抛异常（请求 500，等同于拒绝）。
  修法：出错时按拦截处理并记录日志。
- 说明：计数器为进程内（与参考实现默认的 LocMemCache 同为进程内，等价）。

## 6. 控制台校验

- 参考：`apps/console/middleware.py:39-62`、`config/urls.py:69-82`、`apps/console/views.py:35-46`
- Go：`internal/httpapi/middleware.go:175-180,446-478`、`main.go:149-172`、`internal/web/handlers.go:199-213`

授权条件（登录 + `is_active_status` + `is_operator`）与四种失败响应
（401/401000、403/403001、403/403000、429/429000）逐项一致；限流键
`console_rate:{pk}`、窗口 60s、默认 60、`0` 关闭、在鉴权之后计数，均一致。

死 HMAC 路径**确认未移植**：`internal/` 与 `main.go` 中不存在
`CONSOLE_HMAC`、`X-Console-Timestamp/Signature` 及 401001-401004。
参考路径为 `apps/console/middleware.py:64-130` + `config/settings/base.py:129`
（默认 False）。**接受**：开关按第 7 节不移植；下一节新增的「已移除开关守卫」
会在部署方显式开启它时拒绝启动，避免静默失去该控制。
- 说明：Go 无每个 handler 的二次校验（参考保留 `ConsoleAPIMixin`），
  当前所有 `/console/api/` 路由都在受保护 mux 上，不构成绕过，属纵深防御减弱。

## 7. 上传安全

- 参考：`apps/mediahub/services.py`、`apps/generator/services.py:104-117`、`apps/novelviz/services.py:204-212`
- Go：`internal/media/uploads.go`、`internal/media/storage.go:66-77`、`internal/generator/service.go:398-413`

一致：两种会话类型、参考图 10 MiB / EPUB 80 MiB 上限与会话 TTL、
分片序号边界与长度校验、幂等写分片与幂等完成、`user_id + upload_id` 归属、
状态码与业务码（400/400100、404 HTML、超 4 MiB 请求体 400 HTML）、
越权与不存在的会话一律 HTML 404。路径逃逸：Go 在 `storage.go:66-77` 与
`uploads.go:521-525` 两处强制（参考实现只在 EPUB 目标处强制），**Go 更严**。

- **待修复 P1（参考图只校验文件头）**：参考用 `Image.open(...).verify()`
  做整文件结构校验（`apps/generator/services.py:112-116`），Go 用
  `DecodeConfig` 只读文件头（`internal/media/uploads.go:491` 与
  `internal/generator/provider.go:896-907`）。后果：头部可读但被截断的图片
  会被接受并存储，参考实现以 `参考图无法识别，请重新上传` 拒绝。
  修法：两处改为完整解码。
- **待修复 P2（文案）**：缺少 `total_size` 时参考为 `上传文件不能为空`
  （`apps/mediahub/services.py:284-285`），Go 为 `文件大小不合法`
  （`uploads.go:79-82`）。状态码与业务码相同，仅文案不同。
- **待修复 P2（目录）**：分片上传的 EPUB 落盘目录 Go 写死 `novelviz/uploads`
  （`uploads.go:519`、`media.go:43`），参考用 `settings.NOVELVIZ_UPLOAD_DIR`
  （`apps/novelviz/services.py:205`）；Go 的小说直传路径读了该变量
  （`internal/novel/settings.go:52`）。默认值相同，自定义时会分叉。
- 说明：两侧都不比对声明 MIME 与实际容器（既有弱点，未变）。

## 8. SSRF 防护

- 参考：`apps/mediahub/services.py:157-204,630-674`
- Go：`internal/media/remote.go:16-199`、`internal/media/media.go:46-51`

一致：精确主机名白名单（默认 `s3.siliconflow.cn`，大小写不敏感、忽略端口）、
重定向上限 3 且每跳重新校验、25 MiB 上限、MIME 嗅探、60s 超时、
白名单主机跳过地址检查（与参考相同）。

- **待修复 P1（IPv6 网段）**：`remote.go:153-168` 的 `isGlobalIP` 缺少
  参考（Python `ipaddress.is_global`）会拦的网段：`2002::/16`（6to4，
  可编码任意 IPv4）、`2001::/23`、`100::/64`、`64:ff9b:1::/48`、`3fff::/20`。
  后果：本机若存在 6to4 路由，`http://[2002:0a00:0001::]/…` 可绕过。
  修法：补齐网段（Go 对 `192.0.0.9/.10`、`224/4`、`240/4`、IPv6 组播比参考更严，保留）。
- **待修复 P2（重试分类）**：参考的上游 HTTP 错误/连接错误可重试
  （`apps/generator/tasks.py:26-31`），Go 把远程取图的错误一律判为不可重试
  （`remote.go:29-49` 与 `provider.go:72-80`），且持久化的
  `exception_type` 为 `ValueError` 而非 `HTTPError`/`ConnectionError`。
  后果：上游 CDN 瞬时 502 会直接判任务失败。修法：分类与参考对齐。
- **待修复 P2（大写 scheme）**：Python `urlparse` 会小写化（`HTTP://` 可接受），
  Go 直接拒绝（`remote.go:82-84`）。
- 说明：DNS 重绑定在两侧同样存在（先解析校验、再让 HTTP 客户端解析一次），
  未修复且已在审查清单 P1-23 记录，属**继承的既有风险**。

## 9. 字段加密与密钥日志

- 参考：`apps/core/security.py:10-37`、`apps/console/serializers.py:8-38`
- Go：`internal/crypt/crypt.go`、`internal/siteconfig/siteconfig.go:98-104`、`internal/console/serialize.go:182`

**已修复**：44 字节规范密钥被二次派生（`DeriveFernetKey` 返回解码后的 32 字节，
`newFernet` 再哈希一次），导致与参考实现互不可解——`scripts/export_deploy_bundle.py:127`
生成的正是这种密钥。修法与验证见提交 `fix(S5-crypt)` 与
`internal/crypt/crypt_test.go`（用 `.venv-ref` 的 cryptography 生成交叉向量）。
同提交去掉自加的 `v1:` 前缀，存储形态回到裸 Fernet 令牌。

**已修复**：控制台密钥掩码 Go 用 `******`，参考用 `前 3 + ****** + 后 3`
（`apps/core/siteconfig.py:60-68`），属前端可见差异，改为与参考一致。

日志逐条核对（Go 侧 98 处日志调用、参考侧全部 `logger.*`）：**未发现任何**
记录 API Key、令牌、签名、验证码、密码或解密明文的语句。最接近的几条只记录
渠道 code、配置键名、上游 URL 与错误文本；请求体从不入日志；
验证码仅以 SHA-256 存储。参考实现同样无泄漏。
- 说明：两侧都会记录上游 URL，若渠道 `base_url` 内嵌凭据则都会被记录，
  属既有行为，非本次回归。

## 10. 配额与限额默认值

- 参考：`config/settings/base.py:116-124`、`apps/billing/services.py`
- Go：`internal/config/config.go:312-325`、`internal/quota/service.go`

默认值逐项一致：每日免费 3、每日任务上限 50、最大排队 3、图片保留 30 天、
软删除保留 7 天。冻结/扣减/退回语义、台账备注（除下述两条）、
每日刷新规则（比日期、冻结不动）、管理员调整文案一致。
并发：Go 在 `BEGIN IMMEDIATE` + 单连接内完成读改写，余额不可能超支；
参考实现的 `select_for_update` 在 SQLite 上是空操作（Django 特性标记为不支持），
理论上可超支——**Go 更严**。

- **待修复 P1（取消/删除的台账备注）**：Go 的 `Cancel`/`DeleteHistory` 调用
  `quota.Release`，用默认备注 `任务失败退回`（`internal/generator/service.go:596,615`
  与 `quota/service.go:171`）；参考写 `用户取消任务`
  （`apps/generator/services.py:558`）与 `用户删除运行中任务`（`:573`）。
  金额相同、用户可见的台账文案不同。`ReleaseWithRemark` 已存在但无人调用。
- **待修复 P0（配额原子性）**：参考在同一次事务内写终态并结算配额
  （`apps/generator/services.py:466-468,515-522,557-559,572-574`）；Go 先在
  `store.go:741-771` 的事务里置 `quota_finalized=1`，随后在**另一次事务**里结算
  （`worker.go:287-295`、`service.go:595-599,614-618,718-729`）。中间崩溃会让
  冻结次数永久滞留（后续守卫看到已终态且已 finalize，不会再退回）。
  同类的第二处：`Submit` 在 `service.go:249` 冻结，若 `insertTask` 返回
  非幂等冲突的错误则直接返回，冻结未退回；参考实现整段提交是同一事务。
  修法：把结算并入终态事务（或至少保证退回与状态写入同事务）；
  提交失败路径补退回。
- **待修复 P2（TIME_ZONE 校验）**：`internal/quota/service.go:459-470` 在时区
  非法时告警并回落到 `time.Local`；参考在启动时校验并拒绝启动
  （`config/settings/base.py:88` 一带）。修法：启动即失败并打印原因。
- **接受（记录）**：Go 让 `site.daily_user_task_limit`、`site.max_pending_tasks`、
  `risk.*` 的 DB 行生效，参考实现虽有这些注册项但未接线
  （`apps/core/siteconfig.py:107-116` 仅注册）。默认播种值等于环境变量值，
  故默认行为一致；只有运维显式改过配置行时才会出现「Go 生效、参考不生效」。
  该差异与 S2 冻结的 `siteconfig = DB > env > default` 解析顺序一致，且写入
  控制台后立即生效是注册这些键的本意；**按第 5 节语义保留**，在最终报告单列。

## 11. 任务一致性

- 参考：`apps/generator/services.py`、`apps/generator/tasks.py`
- Go：`internal/generator/store.go`、`service.go`、`worker.go`

状态集合 `queued/running/succeeded/failed/canceled`、全部转移、
租约与 stall 窗口、`execution_attempts`、重试次数与退避
（`min(1.5·attempt,3)`）、可重试分类、取消语义、终态守卫逐项一致。
不可能重复认领：Go 在单事务内重新判定资格并翻转行
（`store.go:522-569`），比参考（依赖 SQLite 写锁）更强。
不会重复扣费：`quota_finalized` 与终态同事务翻转。取消后的任务不可恢复：
两侧的认领谓词都排除 `canceled`。进程崩溃：租约到期后可被重新认领；
若停机超过 stall 窗口，读路径的对账会置 `task_stalled` 并退回冻结。
- 唯一问题是配额结算的原子性，见第 10 项。
- 说明：Go 的认领谓词额外接受 `running AND lease_expires_at IS NULL`
  （`store.go:524`），参考的 worker 认领不接受该形态（仅其内联派发路径接受）。
  只有绕过 worker 写入的行会命中，属**接受**并记录。
- 说明：参考的内联执行（`GENERATION_TASKS_RUN_INLINE`）按第 7 节不移植，
  Go 始终入队，进度里程碑的时间点因此不同，属**预期**。

## 12. 租户隔离

- 参考：`apps/generator/views.py`、`apps/mediahub/views.py`、`apps/novelviz/views.py`、`apps/users/views.py`
- Go：`internal/media/`、`internal/novel/`、`internal/generator/`、`internal/auth/`

所有从请求可达的读、下载、删除路径都带 owner 谓词：任务
（`generator/store.go:164-168,291-337`）、图片（`media/store.go:74-76,89-101`、
`media/images.go:363-406`）、上传会话（`media/store.go:246-272`，每个 handler
传 `httpapi.UserOf(r).ID()`）、小说任务与场景图
（`novel/store.go:274-281,294,395-402`）、历史与账号页
（`auth/handlers.go:325,349,419`、`web/handlers.go:251-308`）。与参考一致。

下载令牌：原创图 `TimestampSigner("<image_id>:<user_id>")`、TTL 300s
（`media/images.go:157-158`、`media/media.go:46`）；预览
`Signer("p:<id>:<user>:<updated_at>")`、无过期（`:151-155,176-189`）。
**两侧都是能力型 URL**：不要求登录、不绑定请求方，但令牌内绑定 owner，
取图时再校验 `id + user_id + 未删除`（`images.go:216-217` ↔
`apps/mediahub/services.py:704-719`）。故令牌在登出后仍有效——**与参考一致的既有性质**，
在最终报告中作为继承风险单列（原创图 5 分钟窗口；预览 URL 对该版本图片不过期）。

逐条排查了「按 id 取用户数据且不带 user_id」的查询（共 9 处），
全部为 worker/后台路径或调用方已用 owner 作用域解析过：
`store.go:372-374`（硬编码 user 0，无调用者，属死代码，S7 清理）、
`store.go:174-176`（无生产调用者）、`store.go:170-172`（调用方已确认归属）、
`novel/store.go:261-272,404-406`（仅 worker）、`novel/handlers.go:335`
（先经 `TaskByIDForUser`）、`media/store.go:128,137,185`（已归属解析或清理任务）、
`auth/auth.go:465`（会话内 id）、`console/store.go:352,407,425`（operator 门禁）。
**未发现可从请求越权到达的路径。**
- 说明：过期图片经仍然有效的签名链接访问时 Go 返回 HTML 404，参考实现抛
  `FileNotFoundError` → 500（`apps/mediahub/services.py:677-679`）；两者都拒绝内容，Go 更干净。
- 说明：取消/删除不存在或他人的任务时 Go 返回 400101 / HTML 404，
  参考实现 `.get()` 抛 `DoesNotExist` → 500；两者都拒绝，Go 更干净。

---

## 附：按严重度排序的修复清单（下一会话按此顺序执行）

| 级别 | 条目 | 位置 | 状态 |
| --- | --- | --- | --- |
| P0 | SSR `POST /login` 绕过登录锁定与失败计数 | `internal/web/handlers.go:421-459` | 待修复 |
| P0 | `MOCK_PROVIDER_ENABLED` 默认 true 且生产无校验 | `internal/config/config.go:358` | 待修复 |
| P0 | 配额结算与终态写入非同一事务 → 冻结滞留 | `internal/generator/worker.go:287-295` 等 | 待修复 |
| P1 | 会话缺 auth-hash，改密不失效其它会话 | `internal/auth/auth.go:620-623` | 待修复 |
| P1 | 登录未轮换 CSRF 令牌 | `internal/auth/auth.go:616-629` | 待修复 |
| P1 | 未实现 `SECURE_SSL_REDIRECT` / `X-Forwarded-Proto` | `internal/config/config.go:254` | 待修复 |
| P1 | 风险事件不落库（prompt 拦截、任务限流） | `internal/risk/service.go:161-168` | 待修复 |
| P1 | 参考图仅校验文件头（参考做整文件校验） | `internal/media/uploads.go:491`、`generator/provider.go:896-907` | 待修复 |
| P1 | `isGlobalIP` 缺 IPv6 网段 | `internal/media/remote.go:153-168` | 待修复 |
| P1 | 邮箱验证码先删后比，一次错即烧掉 | `internal/auth/email_verification.go:89-101,273` | 待修复 |
| P1 | 遗留 pbkdf2 哈希登录后不回写为 argon2 | `internal/auth/auth.go:243-245,526` | 待修复 |
| P1 | 取消/删除的配额台账备注与参考不同 | `internal/generator/service.go:596,615` | 待修复 |
| P2 | SSR 验证码用 math/rand 且不读站点配置覆盖 | `internal/web/pages.go:92-100`、`handlers.go:527` | 待修复 |
| P2 | DB 出错时 IP 规则放行（参考为拒绝） | `internal/risk/service.go:82-86` | 待修复 |
| P2 | CSRF 未做 Origin/Referer 校验 | `internal/httpapi/session.go:302-324` | 待修复 |
| P2 | 生产静态响应缺安全头 | `internal/httpapi/middleware.go:191-198` | 待修复 |
| P2 | 远程取图错误分类不可重试、exception_type 不同 | `internal/media/remote.go:29-49` | 待修复 |
| P2 | 大写 scheme 被拒（参考接受） | `internal/media/remote.go:82-84` | 待修复 |
| P2 | 缺少 `total_size` 文案不同 | `internal/media/uploads.go:79-82` | 待修复 |
| P2 | 分片上传 EPUB 未读 `NOVELVIZ_UPLOAD_DIR` | `internal/media/uploads.go:519` | 待修复 |
| P2 | `TIME_ZONE` 非法时告警回落而非拒绝启动 | `internal/quota/service.go:459-470` | 待修复 |
| P2 | 新增：已移除的安全开关被显式开启时应拒绝启动 | `internal/config/` | 待新增 |

已修复（本会话）：44 字节 FIELD_CRYPT_KEY 二次派生、`v1:` 前缀、
控制台密钥掩码格式。
