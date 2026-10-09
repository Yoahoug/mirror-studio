# 0.0.1 安全清单复核（S5 修复后的最终结论）

复核时间：2026-10-10。本文是 `docs/security-audit-0.0.1.md` 所列 22 项待修 +
3 项已修的**结项台账**：每项给出最终结论、Go 实现位置、验证方式；与审计结论
不一致的地方（复核后更正的条目）单独说明理由。

验证基线：

- `go vet ./...`、`go test ./... -count=1` 全绿（Go 1.27.1）。
- 差分语料回放（两侧同天录制）：main **211 步**、email **22 步**、upstream
  **77 步**，trace 与 snapshot 均逐字节一致。
- 上游请求录制对比：两侧各 **13 次调用**，方法/路径/载荷一致；仅有的字节差
  （`/v1/images/edits` 的参考图 96x96 JPEG，ref 575 B vs go 740 B）是既定的
  编码器豁免（Pillow vs Go image/jpeg）。
- 新增专项测试：
  - `internal/config/config_test.go`（MOCK 默认/生产拒绝/移除开关/SSL 重定向默认值）
  - `internal/web/login_risk_test.go`（SSR 登录风控三例）
  - `internal/generator/quota_atomicity_test.go`（取消/删除/失败/成功的台账与原子性）
  - `internal/auth/session_hash_test.go`（改密失效其它会话、指纹性质）
  - `internal/auth/rehash_test.go`（pbkdf2 登录回写 argon2、验证码先校验后删除）
  - `internal/risk/risk_test.go`（IP fail-closed 等既有断言更新）
  - `internal/httpapi/csrf_origin_test.go`（Origin/Referer 校验三例）
  - `internal/media/remote_error_test.go`、`media_test.go`（取图错误分类、IPv6 网段、
    上传文案与 EPUB 目录）

修复提交：`2cb1c71`（P0）、`d0c71f3`（P1）、`8dd5abf`（P2）。

---

## 12 项清单逐项结论

### 1. 密码哈希 Argon2 参数不降低 —— ✅ 通过

- Go：`internal/auth/auth.go`（参数常量、`HashPassword`、`VerifyPassword`、`Authenticate`）
- 复核结果：参数一致（argon2id / v=19 / m=102400 / t=2 / p=8 / 32 字节摘要）；
  **遗留哈希登录成功即回写**为当前 argon2 参数（Django `hasher.must_update`
  语义），`pbkdf2_sha256` 哈希不再滞留。`VerifyPassword` 注释同步更正。
- 测试：`TestAuthenticateRehashesLegacyPBKDF2`。

### 2. 生产无条件校验、无跳过开关 —— ✅ 通过（Go 更严）

- Go：`internal/config/config.go`（`Load` 唯一校验入口）、`main.go:46-51`
- 复核结果：`SECRET_KEY`/`FIELD_CRYPT_KEY`/`PUBLIC_ORIGIN`/`DATABASE_URL`
  校验不变；`MOCK_PROVIDER_ENABLED` 默认 **false**，非 DEBUG 下开启即拒绝
  启动（对齐 `config/settings/prod.py`）。`ALLOW_INSECURE_PROD` 未移植。
  另：`ALLOW_INSECURE_PROD`、`CONSOLE_HMAC_ENABLED`、
  `GENERATION_TASKS_RUN_INLINE`、`NOVELVIZ_RUN_INLINE`、`MINIO_ENDPOINT`
  显式设置时拒绝启动（防部署方误以为已恢复该控制）。
- 测试：`TestLoadMockProviderDefaultsOff`、`TestLoadRejectsMockProviderInProduction`、
  `TestLoadAllowsMockProviderInDebug`、`TestLoadRejectsRemovedSwitches`、
  `TestLoadRejectsMinioStorage`。

### 3. CSRF / 会话 cookie / 响应头 / HSTS —— ✅ 通过

- Go：`internal/httpapi/session.go`、`middleware.go`、`internal/auth/auth.go`
- 复核结果（5 项待修全部落地）：
  1. **会话 auth-hash**：会话内保存 `sha256("mirror-studio:session-auth:" +
     password_hash)` 派生指纹（`SessionAuthHash`），中间件每请求比对，不匹配
     即 flush——改密/重置后其它会话失效；改密会话自身刷新指纹存活
     （`handleAccountPassword`）。登录/注册写入指纹。
  2. **CSRF 轮换**：API 登录/注册与 SSR 登录成功后 `httpapi.RotateCSRFToken`
     换发新 token + cookie。
  3. **HTTPS 重定向**：`SECURE_SSL_REDIRECT`（默认非 DEBUG 开）+ 
     `SECURE_PROXY_SSL_HEADER_VALUE`，明文请求 301 到 `PUBLIC_ORIGIN`，
     `X-Forwarded-Proto` 判定已加密跳（`requestIsSecure`）。
  4. **CSRF Origin 校验**：`VerifyCSRFOrigin`——Origin 必须等于请求自身
     `scheme://host` 或 `CSRF_TRUSTED_ORIGINS` 条目；https 无 Origin 时要求
     同 Host 的 Referer。新增环境变量 `CSRF_TRUSTED_ORIGINS`。
  5. **静态响应头**：非 DEBUG 下 `/static/` 补 `nosniff` /
     `Referrer-Policy: same-origin` / `COOP: same-origin`；DEV 保持裸头，
     差分逐字节一致不受影响。
- 测试：`TestPasswordChangeInvalidatesOtherSessions`、
  `TestSessionAuthHashChangesWithPassword`、`csrf_origin_test.go` 全组。

### 4. 验证码与邮箱验证码语义 —— ✅ 通过

- Go：`internal/auth/email_verification.go`、`internal/web/handlers.go`
- 复核结果：**邮箱验证码改为先校验、成功后才删除**（`ConsumeRegisterEmailCode`
  + `emailCodeStore.takeCode/dropCode`），一次输错不再烧掉仍有效的码，
  与参考 `apps/users/email_verification.py:275-277` 一致。
  **SSR 验证码改用 `auth.GenerateCaptchaCode`（crypto/rand）**，并经
  `site.login_captcha_enabled` 的 DB → env → default 解析
  （`Service.loginCaptchaEnabled`），math/rand 版本删除。其余码长/字符集/
  TTL/文案维持审计时的一致结论。

### 5. 风控阈值不变 —— ✅ 通过（含两项审计结论更正，见「例外」）

- Go：`internal/risk/service.go`、`internal/web/handlers.go`
- 复核结果：
  - **SSR 登录接入风控**：`POST /login` 走与 API 相同的
    `EnsureLoginAllowed` → 失败 `RecordLoginFailure` → 成功
    `ClearLoginFailures`，顺序对齐 `LoginPageView.form_valid/form_invalid`；
    已锁定的账号无法再经该路径登录，且尝试会计数。
  - **IPBlocked 查库出错 fail-closed**（拒绝请求并记录日志），对齐参考的
    500 拒绝语义。
  - **风险事件落库（审计 P1-4）在复核后更正**：参考的 `prompt_blocked` /
    `task_rate_limited` 写入位于 `submit_generation_task` 的
    `@transaction.atomic` 块内、随后立即抛 `PermissionError` → **整个事务
    回滚，事件从不持久化**。P0 阶段两侧快照均证实 `risk_riskevent` 只有
    `login_failure` 行。Go 保持相同位置与相同结果（不落库），差异验证通过。
    详见下文「例外与理由」第 1 条。
- 测试：`internal/web/login_risk_test.go` 全组；`risk_test.go` 保持
  「prompt 拒绝不落事件行」的断言。

### 6. 控制台校验不变、死 HMAC 路径不移植 —— ✅ 通过（无改动）

- 授权条件与四种失败响应维持一致；HMAC 死路径未移植；新增的移除开关守卫
  在部署方显式设置 `CONSOLE_HMAC_ENABLED` 时拒绝启动（第 2 项）。

### 7. 上传安全不变 —— ✅ 通过

- Go：`internal/media/uploads.go`、`internal/generator/provider.go`
- 复核结果：
  - **参考图整文件校验**：`validateReferenceImageContent`（media）与
    `decodeAndValidateImage`（generator）从 `DecodeConfig`（只读文件头）改为
    `imageutil.Decode` 完整解码，截断图与参考的 `Image.open(...).verify()`
    同样拒绝。
  - **total_size 文案**：`上传文件不能为空`（参考 `int()` 失败同样落到该文案）。
  - **EPUB 分片目录**：`NOVELVIZ_UPLOAD_DIR` 经 `config.NovelvizUploadDir`
    读取（默认 `novelviz/uploads` 不变），与小说直传路径同源。
- 其余分片协议/限额/越权/路径逃逸结论维持审计一致。

### 8. SSRF 防护不变 —— ✅ 通过

- Go：`internal/media/remote.go`
- 复核结果：
  - **IPv6 网段补齐**：`isGlobalIP` 新增拒绝 `2002::/16`（6to4）、
    `2001:0000::/24`–`2001:01ff::/24`（IETF 协议分配，含 Teredo；db8 已有）、
    `100::/64`、`64:ff9b:1::/48`、`3fff::/20`，与 Python `ipaddress.is_global`
    对齐；Go 对 192.0.0.9/.10、224/4、240/4、IPv6 组播更严的既定差异保留。
  - **错误分类**：`FetchRemoteImage` 的全部失败以 `*RemoteImageError` 返回
    （HTTPError / ConnectionError / GuardError），worker 侧
    `isRetryableError` 对前两类重试、`exceptionTypeName` 落
    `HTTPError`/`ConnectionError`/`ValueError`——与 tasks.py 的
    `_is_retryable_error` 与 requests 异常类名对齐。
  - **大写 scheme（审计 P2）复核后不成立**：Go `net/url.Parse` 与 Python
    `urlparse` 同样把 scheme 小写化（`HTTP://` → `http`），`HTTP://Host` 在
    两侧都被接受。验证：`go run` 探针 + Python urlparse 对照。详见「例外」
    第 2 条。
- DNS 重绑定属两侧继承的既有风险，未变（审查清单 P1-23 已记录）。

### 9. 字段加密不变、密钥不进日志 —— ✅ 通过（前次会话已修）

- 44 字节 `FIELD_CRYPT_KEY` 不再二次派生（`2cb1c71` 之前提交 `e7a0437`）、
  控制台掩码格式一致（`537f31d`）；日志无泄漏的 98 处逐条核对结论维持。

### 10. 配额与限额默认值不变 —— ✅ 通过（Go 更严）

- Go：`internal/quota/service.go`、`internal/generator/{service,store,worker}.go`
- 复核结果：
  - **配额结算并入终态事务**：`setTaskSucceeded`（成功扣减）、
    `setTaskFailure`（失败退回）、`cancelTask`（用户取消任务）、
    `deleteTaskHistory`（用户删除运行中任务）都在 `BEGIN IMMEDIATE` 内经
    `quota.TxHandle` 完成，终态与结算之间不再有崩溃窗口。`Submit` 写行失败
    （含幂等冲突之外的错误）补退回冻结。
  - **台账备注**：取消写 `用户取消任务`、删除写 `用户删除运行中任务`、失败写
    `任务失败退回`，金额与参考一致。
  - **TIME_ZONE（审计 P2）复核后不成立**：`main.go` 启动即
    `time.LoadLocation` 失败退出（拒绝启动），`quota.resolveLocation` 的
    告警回落只是测试/降级兜底，生产路径到不了。详见「例外」第 3 条。
- 「Go 让 site.daily_user_task_limit 等行生效」的既有差异按 S2 冻结语义保留。

### 11. 任务一致性不变 —— ✅ 通过（同第 10 项，原子性已修）

- 状态机/认领/租约/重试/取消逐项维持一致；`quota_finalized` 与终态写入、
  配额结算现在同事务。

### 12. 租户隔离不变 —— ✅ 通过（无改动）

- 审计结论「无越权可达路径」维持；`store.go:372-374` 死代码按计划 S7 删除。

---

## 例外与理由（最终报告需单列）

1. **风险事件不落库（审计 P1-4 更正为「与参考一致」）**。审计认为参考会持久化
   `prompt_blocked` / `task_rate_limited` 而要求 Go 补齐。复核参考代码：
   两处 `RiskEvent.objects.create` 都在 `submit_generation_task`（
   `@transaction.atomic`）内部，事件写入后立即 `raise PermissionError`，事务
   回滚带走事件——**参考实现自己也从不持久化这两类事件**。P0 差分两侧快照
   （各只有 3 条 login_failure）与实现阅读双重证实。中期曾按审计实现落库，
   差分快照随即出现 2 行差异而回退。**结论：Go 与参考一致（调用位置相同、
   不落库）**；`login_failure` 事件（在事务外写）两侧都正常落库。

2. **大写 scheme（审计 P2）复核后不成立**。Go `net/url` 与 Python
   `urllib.parse` 都在解析时把 scheme 小写化（探针：`HTTP://Example.com/x`
   两侧均得 `http` + `Example.com`），`remote.go` 的 `parsed.Scheme != "http"`
   检查不会拒绝大写 scheme。无需改动。

3. **TIME_ZONE 启动校验（审计 P2）复核后不成立**。`main.go` 在
   `time.LoadLocation(cfg.TimeZone)` 失败时返回错误并以退出码 1 结束——
   非法时区本就拒绝启动；`quota.resolveLocation` 的回落分支仅服务单元测试与
   显式构造的 service，生产路径不可达。无需改动。

4. **SSR 登录风控的失败计数范围**：与参考一致——`form_invalid` 只在提交了
   username 时计数；锁定前置检查失败（已锁定）同样记一次失败（参考的
   `form_invalid` 在 `form_valid` 抛错后也走该分支）。SSR 页面验证码语义
   （会话作用域、非一次性）维持参考行为。

5. **继承的既有风险**（与参考一致，不属于回归）：
   - DNS 重绑定：取图先解析校验、后由 HTTP 客户端再次解析（审查 P1-23）。
   - 下载/预览令牌为能力型 URL，登出后仍有效（原创图 5 分钟窗口；预览对
     该版本图片不过期）。
   - 渠道 `base_url` 内嵌凭据会随上游 URL 进日志（两侧相同）。
   - 声明 MIME 与实际容器不比对（两侧相同）。

## 剩余风险

- 会话指纹使改密后其它会话在下一次请求时失效，但被盗 cookie 在改密前的窗口
  内仍然有效——这与 Django 的 `_auth_user_hash` 完全同构，无更强做法。
- IPBlocked 的 60s 缓存与敏感词 30s 缓存（审计「接受」项）维持：规则变更
  最长滞后一个窗口生效。
- 上游请求对比中 `/v1/images/edits` 参考图字节差是 Pillow 与 Go 编码器的
  既定豁免（尺寸/格式一致），非本次改动引入。
