# 绘镜 0.0.1 代码审查与优化清单

审查对象：`main` 分支 Django 1.x 后端（8 个 app + `config/` + `templates/` + 两个 SPA）与部署资产。
审查依据：`docs/api-contract-0.0.1.md` 冻结的契约、`main` 分支的 6 个测试文件（1,690 行 / 60 个用例）、
真实录制的差分轨迹（`.reftmp/run-ref/`）。

**决策取值**：`修复`（在对应域的 Go 移植提交中改正）/ `原样移植`（保持与 Python 完全一致的行为）/
`不移植`（删除，不提供等价物）。

**分级**：P0 = 安全或正确性必须修复；P1 = 性能或一致性，修复或给出不修理由；P2 = 风格与冗余，
随所属模块移植顺带处理。

> 变动用户可见行为的修复（§8 要求单列）集中在文末「附录 A」。

---

## 0. 汇总

| 域 | P0 | P1 | P2 | 合计 |
| --- | --- | --- | --- | --- |
| core | 1 | 3 | 2 | 6 |
| users | 0 | 5 | 2 | 7 |
| risk | 0 | 3 | 1 | 4 |
| quota(billing) | 0 | 3 | 2 | 5 |
| generator | 0 | 5 | 3 | 8 |
| mediahub | 0 | 6 | 3 | 9 |
| novelviz | 0 | 5 | 3 | 8 |
| console | 0 | 3 | 2 | 5 |
| audit | 0 | 2 | 1 | 3 |
| web / templates | 0 | 3 | 2 | 5 |
| 部署 | 0 | 3 | 1 | 4 |
| 前端 | 0 | 1 | 2 | 3 |
| **合计** | **1** | **42** | **24** | **67** |

---

## 1. core

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P0-1 | `apps/core/security.py:12-19` | `FIELD_CRYPT_KEY` 为空时 `encrypt_text` 直接返回明文、`decrypt_text` 原样返回，**渠道 API Key 落库为明文**；且 `decrypt_text` 吞掉 `InvalidToken` 返回密文本身，密钥轮换后静默产生垃圾密钥 | **P0** | **修复** | 无（见附录 A-1） |
| P1-1 | `apps/core/middleware.py` | `X-Request-Id` 生成 `uuid4().hex[:16]`，与 Django 的 32 位 UUID 风格不一致，但仅用于日志关联，前端不消费 | P2 | 原样移植 | 无 |
| P1-2 | `config/urls.py:20-31` + `apps/core/views.py` | `HomeView`/`PricingView` 未注册路由 → `/pricing` 恒 404；`templates/public/home.html`、`public/pricing.html`、`partials/package_cards.html` 为死模板；`_workspace_asset_version()` 用资产文件 `st_mtime_ns` 充当版本号，重新构建即变 | P1 | **不移植**（保留 404） | 无（继续保持 404；不新增页面） |
| P1-3 | `apps/core/siteconfig.py:227` | `except Exception: logger.warning(...)` 包住整个 `get_config`，DB 故障时静默回落到 env/default，掩盖真实错误 | P1 | 修复（降级为：仅 `OperationalError` 回落并记录，其余上抛） | 无 |
| P2-1 | `apps/core/siteconfig.py:CONFIG_KEYS` | 27 个键里的 `risk.*` 会被 console 读写，但 `apps/risk/services.py` 只读 `settings.RISK_*`，形成**死配置** | P2 | **修复**（见附录 A-2） | 有（附录 A-2） |
| P2-2 | `apps/core/api.py` | `api_error` 默认 `code=400000`，但所有调用点都显式传码，默认值从未生效 | P2 | 不移植默认参数（Go 显式传码） | 无 |

## 2. users

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-4 | `templates/account/register.html:118-124` | 发送验证码的 JS 只发 `{email}`，而 `RegisterEmailCodeForm` 要求 `username`+`email` 同时存在 → 该按钮在 `email.verification_enabled=1` 时**必然失败** | P1 | **原样移植** | 无（§4/S4 要求 SSR 页面字节一致；修复会改变渲染输出。见附录 B-1） |
| P1-5 | `apps/users/email_verification.py` | 验证码与失败/发送计数存在 `LocMemCache`：多 worker 部署下各进程独立，A 进程发的码 B 进程验不过；重启即清空，锁定可被重启绕过 | P1 | 修复（Go 单进程内存存储，语义在多 worker 下反而更严格一致） | 无 |
| P1-6 | `apps/users/views.py:264-266` | `AccountPasswordAPIView` 与 SPA 的字段名不一致风险：真实字段是 `current_password/new_password/confirm_password`（不是 `old_password/password1/password2`） | P1 | 原样移植（已在契约中固化） | 无 |
| P1-7 | `apps/users/views.py:399-410` | 邮件异常映射混乱：`ImproperlyConfigured` → 500/500001，`EmailVerificationSendError` → **502**/500002，两者语义相反（HTTP 502 配 code 500002 = "发送失败"） | P1 | 原样移植（契约已固化状态码） | 无 |
| P1-8 | `apps/users/views.py:441-450` | 登录失败累加只在**表单校验失败**时发生；风控前置 429 分支不再累加，导致长期锁定时计数不再刷新（锁定到期后计数已过期） | P1 | 原样移植（行为差异会改变锁定窗口语义） | 无 |
| P2-3 | `apps/users/forms.py:131` | `validate_password(password)` 不传 `user`，因此注册与重置密码**不会**触发 `UserAttributeSimilarityValidator` | P2 | 原样移植（语义已在契约固化：`密码跟 用户名 太相似了。` 仅出现在改了用户名的场景） | 无 |
| P2-4 | `apps/users/utils.py` | 验证码 SVG 由码值确定性生成，`data:` URI 里编码了码值；仅在 session 已持有码时才有意义，不构成泄漏，但会随响应体进入日志抓取面 | P2 | 原样移植 | 无 |

## 3. risk

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-9 | `apps/risk/middleware.py` | `IPBlockMiddleware` 作用于**全部路径**（含 `/healthz`），封禁 IP 连健康检查都拿不到 200，会给探活/负载均衡造成误判 | P1 | 原样移植（放行 `/healthz` 会改变安全边界，属安全项，不降级） | 无 |
| P1-10 | `apps/risk/services.py` | 计数同样存 `LocMemCache`（同 P1-5）：多 worker 下阈值被放大 N 倍 | P1 | 修复（同 P1-5） | 无 |
| P1-11 | `apps/risk/services.py:_login_key` | 键 `risk:login:{username}:{ip}` 对每个 (用户名, IP) 组合独立计数 → 攻击者轮换用户名即可绕过锁定；且键空间无上限 | P1 | 原样移植（改为 IP 维度会改变阈值语义，属安全项变更） | 无 |
| P2-5 | `apps/risk/models.py:SensitiveWordRule` | `contains` 规则用 `keyword in text` 逐条 Python 判断，无候选索引；词表规模大时是 O(n) 全量扫描 | P2 | 修复（Go 预编译规则集，仍在内存中线性匹配，规模由其自身上限约束） | 无 |

## 4. quota（原 billing）

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-12 | `apps/billing/services.py:refresh_daily_quota` | 跨天重置用 `timezone.localdate()`，即**服务器本地时区**；容器 `TZ` 变化会改变重置时刻 | P1 | 原样移植（改为 UTC 会改变语义） | 无 |
| P1-13 | `apps/billing/services.py` | 账本号 `L%Y%m%d%H%M%S` + 8 位十六进制，同秒内理论碰撞概率极低但非零；且 `ledger_no` 唯一约束意味着碰撞会 500 | P1 | 修复（Go 用同一格式，碰撞时重试一次） | 无 |
| P1-14 | `apps/billing/models.py` | `QuotaAccount` 无 `select_for_update` 保护的所有读取路径（`get_quota_summary`）与写入路径（冻结/消费）分离，Postgres 下靠事务内的 `select_for_update` 保证；SQLite 下退化为无锁 | P1 | 修复（Go 用 `BEGIN IMMEDIATE` + 单条 `UPDATE ... RETURNING`） | 无 |
| P2-6 | `apps/billing/urls.py`、`views.py`、`tasks.py`、`gateways/` | 全部为 docstring 占位文件，`urlpatterns = []` | P2 | **不移植** | 无 |
| P2-7 | 目录名 `billing` | 名不副实（无任何计费，只有配额） | P2 | 重命名为 `internal/quota` | 无（内部包名） |

## 5. generator

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-15 | `apps/generator/models.py:127-131` | 缺少 `(status, lease_expires_at)` 与 `(status, submitted_at)` 索引；worker 每轮认领都做全表扫描（`queued` OR `running` 且租约过期），任务表增长后轮询成本线性上升 | P1 | 修复（Go 迁移里补索引） | 无 |
| P1-16 | `apps/generator/services.py:300` | `select_for_update()` 在 SQLite 上是空操作 → SQLite 部署下**并发认领可能重复执行同一任务**（差分基线正是 SQLite） | P1 | 修复（Go 用 `UPDATE ... WHERE status = ... RETURNING` 原子认领） | 无 |
| P1-17 | `apps/generator/views.py:171-205` | `_wait_for_task` 在请求线程内最长阻塞 10s 并以 0.5s 间隔轮询，同时**写入** `progress_percent`（读请求产生写事务） | P1 | 原样移植（SPA 依赖该行为驱动进度显示） | 无 |
| P1-18 | `apps/generator/providers.py:527` | `except Exception:` 包住响应裁剪，任何解析异常都退化为"裁剪失败"并静默 | P1 | 修复（记录 warning 后走同一降级） | 无 |
| P1-19 | `apps/generator/providers.py:428` | openai 图生图在 400/404/405/415/422 时降级到 JSON `image_url` 重试一次；降级失败时**掩盖原始错误**（保留最后一次异常） | P1 | 原样移植（错误分类已固化） | 无 |
| P2-8 | `apps/generator/providers.py:MockImageProviderAdapter` | 依赖 Pillow 绘制 mock PNG | P2 | 修复（Go 用 `image/png` 标准库；仅影响测试夹具） | 无 |
| P2-9 | `apps/generator/tasks.py:187,198` | `dispatch_generate_image_task` 内层 `except Exception` 仅记日志；`GENERATION_TASKS_RUN_INLINE` 与 `CELERY_TASK_ALWAYS_EAGER` 双开关表达同一件事 | P2 | 不移植开关（Go 只有"进程内 worker"一种模式） | 无 |
| P2-10 | `apps/generator/services.py:serialize_task` | `include_reference` 参数只有一个调用点传 `False`（其余默认 `False`），实际等于恒 `False` | P2 | 不移植该参数 | 无 |

## 6. mediahub

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-20 | `apps/mediahub/services.py:221,673,740,760` | 4 处 `except Exception` 静默吞掉（缩略图生成、对象读取等），磁盘/存储故障无任何痕迹；同文件 516/546/790 虽有日志但级别与退出条件不统一 | P1 | 修复（一律 `logger.warning(..., exc_info=True)` 后再走降级分支，返回行为不变） | 无 |
| P1-21 | `apps/mediahub/services.py:issue_download_url` | 预览 URL 用**不含时间戳的稳定签名**，永不过期；仅靠"登录用户 == token 归属者"约束 | P1 | 原样移植（缩短有效期会破坏 SPA 缓存的预览 URL） | 无 |
| P1-22 | `apps/mediahub/services.py:expire_*` / `purge_deleted_image_files` | 清理任务与"正在被下载的图片"之间存在竞态：清理只依据状态与到期时间，不检查是否正在读取 | P1 | 修复（Go 清理时分批 + 先置状态再删文件，删除失败下次重试；语义不变） | 无 |
| P1-23 | `apps/mediahub/services.py:_validate_remote_image_url` | 先 `getaddrinfo` 校验再交给 `requests` 重新解析 → DNS rebinding 的理论窗口；`ipaddress.is_global` 判定已阻断私网/保留段 | P1 | 原样移植（残余风险不变，见报告「剩余风险」） | 无 |
| P1-24 | `apps/mediahub/services.py:_assemble_upload` | 合并分片后校验 sha256 但**不校验客户端声明的 sha256**（协议里没有该字段），因此"校验"只是记录 | P1 | 原样移植（协议未定义该字段，新增字段属改接口） | 无 |
| P1-25 | `apps/mediahub/views.py:71` | `/api/images` 硬编码上限 100 且**不分页**；超过 100 张后老图在前端"消失"（无翻页入口） | P1 | 原样移植（加翻页＝改接口；SPA 没有对应 UI） | 无 |
| P2-11 | `apps/mediahub/services.py` | `minio` 与本地文件系统双存储分支（`storage.mode`），生产 compose 恒 `MINIO_USE_LOCAL_FS=1`，MinIO 分支从未启用 | P2 | **不移植** | 无 |
| P2-12 | `apps/mediahub/services.py:733-745` | `_serve_preview_object` 的"预生成缩略图 → 即时生成"两级回退是必要链路，不是死分支 | P2 | 原样移植 | 无 |
| P2-13 | `apps/mediahub/models.py:UploadSession.uploaded_chunks` | 分片索引存在 JSON 字段里，`store_upload_chunk` 每次都要读出全量、去重、排序、整体写回 | P2 | 原样移植（分片数上限由 `chunk_size` 约束，实测规模内无问题） | 无 |

## 7. novelviz

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-26 | `apps/novelviz/views.py:221-258` | SSE 每连接每 `NOVELVIZ_SSE_POLL_SECONDS` 查一次库，并**把整段 `logs` JSON 反序列化**（已用 `values()` 收窄投影，但仍随日志条数线性） | P1 | 修复（Go 用内存事件环形缓冲 + `sync.Cond` 通知，DB 只作为重启后的兜底；事件名/载荷/裁剪规则不变） | 无 |
| P1-27 | `apps/novelviz/services.py` `_generate_scene_images` | 用 `asyncio` + `Semaphore` 实现并发，却在同步 Django 视图里通过 `asyncio.run` 驱动；失败重试次数依赖任务级 `attempts`，与生成任务的重试策略不同源 | P1 | 修复（Go 用 goroutine + 带缓冲 channel，重试语义与 `_is_retryable_error` 对齐） | 无 |
| P1-28 | `apps/novelviz/services.py:239,272,828` | 3 处 `except Exception` 静默（书籍路径解析、EPUB 抽取、LLM 响应解析），损坏 EPUB 会得到空场景列表而不是错误 | P1 | 修复（记录后返回明确错误，用户可见文案与 `400102` 一致） | 无 |
| P1-29 | `apps/novelviz/views.py:107-118` | `GET /api/novelviz/tasks` 先取 30 条、逐条 `_rescue_stalled_task`、再**重新查询一次**同一批数据 → 每次列表请求 2 次查询 + 最多 30 次写 | P1 | 修复（合并为一次查询；复活逻辑只在写入路径触发） | 无 |
| P1-30 | `apps/novelviz/services.py:SCENE_KEYS` | 场景字段别名表（`编号/中文标题/所在章节/…` 与英文键并存）是对**不可控外部 LLM 输出格式**的输入归一化，不是兼容别名 | P1 | 原样移植 | 无 |
| P2-14 | `apps/novelviz/services.py:logs` 上限 120 | 裁剪发生在写入时（超出裁掉最旧），读路径直接返回 | P2 | 原样移植 | 无 |
| P2-15 | `apps/novelviz/views.py:60-79` | `_build_scene_preview_content` 在预览未命中缓存时同步做 LANCZOS 缩放，可能阻塞 SSE 之外的图片请求 | P2 | 修复（Go 在场景成功时就生成缩略图，与 `preview_image_path` 语义一致） | 无 |
| P2-16 | `apps/novelviz/services.py` | `NOVELVIZ_*` 环境变量存在多条别名回退链（如 `NOVELVIZ_IMAGE_MODEL` / `NOVELVIZ_DEFAULT_MODEL` / `MODEL_NAME`） | P2 | **不移植**（仅保留语义独立的 size/quality/concurrency/scene count/directory） | 无 |

## 8. console

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-31 | `apps/console/middleware.py:ConsoleSecurityMiddleware` | 安全响应头与限流只作用于 `/console/api/` 前缀，`/console`（SPA 外壳 HTML）没有 `Cache-Control: no-store` | P1 | 原样移植（扩大范围会改变响应头契约，而安全头属"不得降级但也不得擅自变更"项） | 无 |
| P1-32 | `apps/console/middleware.py` | `CONSOLE_HMAC_ENABLED` 分支是**不可达死代码**（开关从未在 settings 中定义，`getattr` 恒取默认 `False`） | P1 | **不移植** | 无 |
| P1-33 | `apps/console/views.py:53-72` | `_paginate` 对超大 `per_page` 静默钳制到 100，不告知调用方；与业务列表的 `page_size`（钳 48）命名与上限都不一致 | P1 | 原样移植（两套参数名都已固化在契约里） | 无 |
| P2-17 | `apps/console/views.py:113` | `SiteConfig.objects.all().order_by("category","key")` 无分页，配置项总数（27）远小于任何风险阈值 | P2 | 原样移植 | 无 |
| P2-18 | `apps/console/views.py:233` | 删除渠道时检查 `channel.tasks.exists()`，但任务表只有 `channel` 外键而无相应索引 | P2 | 修复（Go 迁移中为 `generator_generatetask.channel_id` 建索引） | 无 |

## 9. audit

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-34 | `apps/audit/models.py` | `CallbackEventLog` **没有索引、没有保留策略**，只增不删；console 的失败日志页每次都全表扫描并按 `created_at` 排序 | P1 | 修复（Go 迁移补 `(created_at)` 与 `(event_type, created_at)` 索引；不引入保留策略，避免新增行为） | 无 |
| P1-35 | `apps/audit/services.py:8`、`apps/audit/models.py:8` | `log_admin_operation` **零调用**（全仓唯一引用是它自己的定义），`AdminOperationLog` 仅被 models / 迁移 / 即将删除的 Django admin 引用 → 该表永远是空的，是死能力。同时 console 的所有写操作（额度调整、渠道增删改、配置读写）**完全没有任何审计记录** | P1 | **不移植** `log_admin_operation` 与 `AdminOperationLog`（`apps/audit/admin.py` 亦随之删除）；`CallbackEventLog` 原样移植 | 无（改前也没有任何审计记录） |
| P2-19 | `apps/audit/services.py` | `event_type` 取值散落在各调用点字符串字面量里，无枚举约束 | P2 | 原样移植（Go 以常量集中定义，落库值不变） | 无 |

## 10. web / templates

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-36 | `templates/` | 6 个模板不可达或不含信息：`public/home.html`、`public/pricing.html`、`partials/package_cards.html`、`account/orders.html`、`account/quota.html`、`console/dashboard.html`、`billing/payment_checkout.html` | P1 | **不移植** | 无 |
| P1-37 | `templates/base.html`、根 `package.json` | `base.html`（7.5KB）内含一个 `<style>` 块并引用 `static/css/main.css`；后者由根目录 Tailwind 管线产出（当前 74KB）。存在**两条**样式来源，但 `base.html` 的引用是单一入口，不构成重复管线 | P1 | 保留该唯一 CSS 管线（根 `vite/tailwind` 构建 + `static/css/main.css`），`base.html` 逐字保留，保证 SSR 页面零差异 | 无 |
| P1-38 | `templates/account/register.html:35` | `data-cooldown="{{ email_code_resend_seconds }}"` 与 API 返回的 `resend_seconds` 双来源，且两者取不同配置 | P1 | 原样移植（只渲染不消费） | 无 |
| P2-20 | `templates/` | 模板里出现 3 处 `{% include %}` + 1 处 `{% extends %}` 层级，无重复块 | P2 | 原样移植 | 无 |
| P2-21 | `templates/account/security.html` | 安全设置页同时渲染修改密码表单与验证码预览，但页面 JS 未使用验证码 | P2 | 原样移植（删除会改变 DOM） | 无 |

## 11. 部署资产

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-39 | `docker-compose.yml` | 3 个服务、3 个卷（postgres + web + worker），worker 与 web 镜像相同却各跑一份 Python，空载合计约 126 MiB，镜像 698 MB | P1 | 修复（单容器 + 单卷 + 单二进制） | 无 |
| P1-40 | `docker/start-web.sh` | 4 个 `AUTO_*` 开关 + `wait_for_db.py` 轮询；开关两两组合产生 16 条启动路径 | P1 | **不移植**（Go 启动固定为：迁移 → 幂等种子 → 监听；失败即退出并打印原因） | 无 |
| P1-41 | `scripts/make_update_zip.py`、`scripts/export_deploy_bundle.py`、`docker/nginx/`、`docker/start-beat.sh` | 与 `RELEASE_PUSH_*`、`WORKSPACE_RELEASE_*` 硬编码配套的离线更新包流程；beat 进程无任何已注册任务 | P1 | **不移植**（版本单源于 `VERSION` + `CHANGELOG.md`） | 无 |
| P2-22 | `requirements/base.txt` | 14 条运行时依赖中 `minio`、腾讯云 SDK、`aiohttp`、`Pillow`、`ebooklib`、`beautifulsoup4`、`psycopg` 均在"不移植"之列 | P2 | 不移植 | 无 |

## 12. 前端

| ID | 位置 | 问题 | 级别 | 决策 | 用户可见影响 |
| --- | --- | --- | --- | --- | --- |
| P1-42 | `frontend/workspace/src/services/geminiService.ts`、`frontend/console/src/services/geminiService.ts` | 各 3 行的死桩（调用即抛错），无任何引用 | P1 | 删除（§4 允许的"删除未被引用的代码"，以截图零差异为门禁） | 无 |
| P2-23 | `frontend/workspace` 与 `frontend/console` | 两份 `package.json` / lockfile / node_modules，`apiService.ts` 与 UI 原语高度重复 | P2 | 合并为单包（§4 允许的"消除复制粘贴 + 合并包与构建管线"） | 无 |
| P2-24 | `frontend/console/src/components/` | 存在与 workspace 同名的不可达副本组件 | P2 | 删除（同上） | 无 |

---

## 8 节强制检查项对照

| 强制检查项 | 结论 | 对应条目 |
| --- | --- | --- |
| 任务状态机：并发认领重复处理 | **存在**（SQLite 下 `select_for_update` 为空操作） | P1-16 |
| 任务状态机：超时边界与卡死重排 | 逻辑完整（租约 + `requeue_task_after_worker_loss` + `reconcile_task_runtime_state`），边界 `lease = max(stalled,180)` | 原样移植 |
| 任务状态机：失败原因用户可读 | `build_public_error_message` 统一为公开文案，不泄漏上游细节 | 原样移植 |
| 任务状态机：重试次数上限 | 有，`channel.max_retries` + `_is_retryable_error` | P1-18 |
| 额度：并发扣减原子性 | Postgres 下成立，SQLite 下退化 | P1-14 |
| 额度：跨天/时区 | `localdate()` 依赖服务器时区 | P1-12 |
| 额度：超发与回滚 | `quota_finalized` + `unfreeze` 覆盖，无超发路径 | 原样移植 |
| 上传：会话 TTL 与并发 complete | TTL 有；并发 complete 靠行锁，SQLite 下降级；`completed` 幂等返回 | P1-16、P1-24 |
| 上传：重复 complete / 断点续传一致性 | 两者均正确（幂等返回 + `client_upload_id` 续传 + sha256 记录） | 原样移植 |
| 上传：路径穿越 | 安全（`Path(...).name` + `upload_id` 为 `uuid4().hex` + `index` 为 int 且校验范围） | 原样移植 |
| 媒体：软删除 + 保留期计算 | 状态机 `available/expired/deleted` + `deleted_at` + 批量清理 | P1-22 |
| 媒体：孤儿文件 | 上传会话文件在 complete/cancel/expire 三处清理；对象存储的对象在软删除清理时删除，存在"DB 行已删、对象删除失败"的孤儿窗口 | P1-22 |
| 媒体：预览与原图一致性 | 预览在存储时一次性生成并落独立对象，读取优先命中 | P2-12 |
| 媒体：清理与在用图片竞态 | 存在（只依据状态与时间） | P1-22 |
| 小说：事件日志裁剪 | 上限 120，超出裁最旧，载荷清洗完整 | 原样移植 |
| 小说：SSE 轮询成本 | 每连接每周期一次库查询 + 全量 `logs` 反序列化 | P1-26 |
| 小说：场景并发与失败重试 | 并发 1..8 有信号量；重试与生成任务策略不同源 | P1-27 |
| 小说：畸形 EPUB 输入 | 有 `400102` 分支，但 3 处静默吞异常使错误信息退化 | P1-28 |
| 风控：计数并发 | 单进程内原子，多进程下各算各的 | P1-10 |
| 风控：XFF 信任边界 | 仅在 `TRUST_X_FORWARDED_FOR` 为真时取最左值，边界正确 | 原样移植 |
| 风控：锁定/解锁时机 | 阈值 + 窗口 TTL，解锁靠过期；429 分支不续期 | P1-8、P1-11 |
| SSRF：白名单判定 | 精确主机名白名单，命中即放行（跳过 IP 校验），符合"显式信任"语义 | 原样移植 |
| SSRF：重定向跟随 | 手动逐跳，每跳重新校验，上限 3 | 原样移植 |
| SSRF：DNS rebinding | 解析后校验、连接时重解析，存在 TOCTOU 理论窗口 | P1-23 |
| SSRF：体积上限 | `MAX_REMOTE_IMAGE_BYTES`（25MB）流式截断 | 原样移植 |
| SSRF：内容类型校验 | 按 MIME 白名单 + 嗅探 | 原样移植 |
| 密钥：`FIELD_CRYPT_KEY` 为空 | **明文存储** | **P0-1** |
| 密钥：密文版本化 | 无版本前缀，密钥轮换不可识别（Fernet token 自带版本字节，但无应用级标识） | P0-1 |
| 密钥：日志泄漏 | 抽查 30 处 logger 调用，未发现密钥、令牌、验证码入日志；验证码在入库前已做 sha256 摘要；`generator.providers._trim_openai_image_response` 会把 b64 图片替换为占位标记后才写 `raw_response` | 原样移植 |
| 查询效率：列表 N+1 | `select_related`/`prefetch_related` 覆盖主要列表；`/api/images` 有 `select_related("task")` | 原样移植 |
| 查询效率：缺失索引 | 任务认领路径、审计表、任务 `channel_id` | P1-15、P1-34、P2-18 |
| 查询效率：全表扫描 | 见上 | 同上 |
| 查询效率：分页 | 业务列表 24/48，console 20/100，`/api/images` 与 `/api/novelviz/*` 不分页 | P1-25、P1-33 |
| 防御性代码：单值开关 | `GENERATION_TASKS_RUN_INLINE`、`CELERY_TASK_ALWAYS_EAGER`、`AUTO_*`、`CONSOLE_HMAC_ENABLED` | 不移植 |
| 防御性代码：兼容别名 | `NOVELVIZ_*` 多级回退链 | P2-16（不移植）；`SCENE_KEYS` 属输入归一化，保留 |
| 防御性代码："以后可能用到"分支 | MinIO 双存储、`InternalReleasePublishAPIView`、billing 占位模块 | 不移植 |
| 防御性代码：`except Exception: pass` 静默 | 3 处真静默（均为 `novelviz/views.py` 里的 `_rescue_stalled_task` 尽力而为调用）；另有 15 处 `except Exception` 块内无任何日志（`console/serializers.py:47,76`、`generator/providers.py:527`、`generator/tasks.py:139`、`mediahub/services.py:221,673,740,760`、`novelviz/services.py:828`、`novelviz/views.py:65,110,152,182,200,214`） | P1-20、P1-28、P1-3、P1-18 |
| 边界输入校验 | 保留（尺寸规范化、上传长度/类型、URL 白名单、路径穿越校验） | 原样移植 |
| 安全/一致性机制 | 保留（CSRF、会话属性、风控、SSRF、签名 URL、租户隔离） | 原样移植 |
| 后台循环顶层捕获 | 保留（worker 循环 `except Exception` + 退避日志） | 原样移植 |
| 有用户可见行为的失败分支 | 保留（全部业务码与文案已在契约固化） | 原样移植 |

---

## 附录 A：会改变用户可见行为的修复（§8 要求单列）

### A-1（原 P0-1）密钥加密：明文存储 → 启动即强制要求密钥

- **背景**：`FIELD_CRYPT_KEY` 为空时渠道 API Key 明文入库。
- **选择**：0.0.1 把 `FIELD_CRYPT_KEY` 提升为**必填配置**，缺失或长度不足时启动失败并打印原因；
  密文加应用级版本前缀 `v1:`，解密时校验前缀，前缀缺失或解密失败返回明确错误而非静默返回密文。
- **前后差异**：改前 = 空密钥时可正常启动且密钥明文落库；改后 = 空密钥直接拒绝启动。
  运维上多了一个必填项，但这是 §9「字段加密不降级」的必然要求。
  解密失败的影响面：改前是"上游 401、任务失败"；改后是"未调用上游即任务失败"，
  用户看到的结果相同（任务失败 + 统一公开文案）。

### A-2（原 P2-1）风控配置：死配置 → 真实生效

- **背景**：console 的配置页展示并可保存 `risk.login_fail_limit` 等 4 个 `risk.*` 键，
  但 `apps/risk/services.py` 只读 `settings.RISK_*`，保存后无任何效果。
- **选择**：Go 的风控服务按标准解析顺序读配置（DB 行 → 环境变量 → 默认值），
  使 console 上的编辑真实生效（缓存窗口 60s，与 `get_config` 一致）。
- **前后差异**：改前 = 在 console 修改风控阈值不生效；改后 = 立即生效（至多 60s 后生效）。
  差分语料中 `risk.*` 的 SiteConfig 行不存在（回落到与 Python 相同的环境变量值），
  且风控相位在 console 相位之前执行，因此该修复对差分验证无影响。

## 附录 B：审查中发现但**不修**的问题（含理由）

### B-1（原 P1-4）SSR 注册页发送验证码按钮必然失败

- **背景**：`templates/account/register.html` 的 JS 只提交 `{email}`，而 `RegisterEmailCodeForm`
  要求 `username` 与 `email` 同时存在 → 该按钮在 `email.verification_enabled=1` 时必然返回 400。
- **不修的理由**：§2 把「前端不改动」列为优先级第 2 的不可谈判项；§4 明确要求 SSR 页面
  **逐字复刻**（规范化 DOM 比对 + 截图比对，不一致即判失败），修复模板 JS 会改变渲染输出。
  §8 允许修复正确性缺陷，但要求「改动用户可见行为的修复单列并有前后差异说明」——
  本项确实满足单列条件，但与之冲突的 §4 是不可谈判项，按优先级必须让位。
- **用户可见影响**：维持现状。默认部署 `EMAIL_VERIFICATION_ENABLED=0` 时该按钮所在区块
  （`{% if email_verification_enabled %}`）根本不渲染，因此**默认部署下完全不可见**。
- **建议**：作为 0.0.2 的独立前端修复单独立项，不夹带在本次重写中。

## 附录 C：契约文档与代码不一致处的裁定

| 项 | 规格书描述 | 代码事实 | 裁定 |
| --- | --- | --- | --- |
| `_captcha_preview.html` | 列为「重构后不再使用的模板」 | 被 `login.html`、`forgot_password.html`、`security.html` 三处 `{% include %}` | **保留并移植**（删除会破坏三个页面的 DOM 零差异） |
| 「SSR 页面」清单含 `public/home`、`public/pricing` | 要求逐字复刻 | 两个视图未注册路由，`/pricing` 恒 404 | **不移植模板**，保留 404 |
| 业务列表分页 | 未给出参数 | `page_size` 默认 24、钳制 1..48 | 以代码为准（契约 §0.5） |
| 任务限流业务码 | 未给出 | **400403 / HTTP 403**（不是 400429） | 以代码与实测为准（契约 §3） |
| 登录锁定文案 | 未给出 | `登录失败次数过多，请 15 分钟后再试`（分钟，不是秒） | 以实测为准 |
