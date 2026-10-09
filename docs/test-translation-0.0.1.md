# 测试翻译清单（0.0.1）

行为规格来源：main 分支 6 个 Django 测试文件，共 **1690 行 / 60 个测试**。
本文件是翻译率的唯一台账：每个 Django 测试对应一个 Go 测试函数，逐条核对断言。

翻译率口径：
- **纳入范围** = 60 − 2（`InternalReleasePublishAPITests`，接口整体在不移植清单） = **58**
- **翻译率 = 已翻译测试数 / 58**，目标 100%
- 未翻译项必须逐条列在「遗漏清单」并说明理由

Go 测试文件与被测包一一对应，放在各自包内（`_test.go`），使用 `testing` + `net/http/httptest`，
不引入任何测试框架依赖。每个 Go 测试函数上方以注释标注来源 Django 测试，例如：

```go
// 来源: apps/generator/tests.py::GeneratorFlowTests::test_generate_failure_releases_frozen_quota
```

## 1. apps/core/tests.py（5 → 3）

| Django 测试 | Go 测试 | 目标包 |
| --- | --- | --- |
| `ChatAPIViewTests::test_chat_uses_selected_llm_channel` | `TestChatUsesSelectedLLMChannel` | `internal/provider` |
| `ChatAPIViewTests::test_chat_requires_available_llm_model` | `TestChatRequiresAvailableLLMModel` | `internal/provider` |
| `ChatAPIViewTests::test_chat_models_only_returns_llm_channels` | `TestChatModelsOnlyReturnsLLMChannels` | `internal/provider` |
| `InternalReleasePublishAPITests::test_publish_release_updates_site_config` | — | 不移植（release-push 接口） |
| `InternalReleasePublishAPITests::test_publish_release_rejects_invalid_signature` | — | 不移植（release-push 接口） |

`ChatAPIViewTests` 断言要点：选中渠道的 base_url / model / api_key 被使用；未配置 key 时返回
400 `未配置可用的 LLM 模型`；`/api/chat/models` 只列出 `channel_type=llm` 且激活的渠道。

## 2. apps/users/tests.py（7 → 7）

| Django 测试 | Go 测试 | 目标包 |
| --- | --- | --- |
| `RegisterEmailVerificationTests::test_send_register_email_code` | `TestSendRegisterEmailCode` | `internal/auth` |
| `RegisterEmailVerificationTests::test_send_register_email_code_has_cooldown` | `TestSendRegisterEmailCodeCooldown` | `internal/auth` |
| `RegisterEmailVerificationTests::test_send_register_email_code_rejects_duplicate_username` | `TestSendRegisterEmailCodeRejectsDuplicateUsername` | `internal/auth` |
| `RegisterEmailVerificationTests::test_register_check_can_report_duplicate_username` | `TestRegisterCheckReportsDuplicateUsername` | `internal/auth` |
| `RegisterEmailVerificationTests::test_register_page_requires_valid_email_code_and_consumes_it` | `TestRegisterRequiresAndConsumesEmailCode` | `internal/auth` |
| `LoginSecurityTests::test_login_rejects_external_next_url` | `TestLoginRejectsExternalNextURL` | `internal/auth` |
| `LoginSecurityTests::test_login_api_rejects_invalid_json` | `TestLoginAPIRejectsInvalidJSON` | `internal/auth` |

## 3. apps/generator/tests.py（22 → 22）

| Django 测试 | Go 测试 | 目标包 |
| --- | --- | --- |
| `GeneratorFlowTests::test_generate_success_consumes_frozen_quota_and_persists_image` | `TestGenerateSuccessConsumesFrozenQuotaAndPersistsImage` | `internal/generator` |
| `GeneratorFlowTests::test_duplicate_client_request_id_returns_existing_task_without_extra_quota` | `TestDuplicateClientRequestIDReturnsExistingTask` | `internal/generator` |
| `GeneratorFlowTests::test_task_create_wait_returns_full_progress_payload` | `TestTaskCreateWaitReturnsProgressPayload` | `internal/generator` |
| `GeneratorFlowTests::test_submit_generation_task_accepts_2k_custom_size_and_quality` | `TestSubmitAccepts2KCustomSizeAndQuality` | `internal/generator` |
| `GeneratorFlowTests::test_submit_generation_task_rejects_invalid_size_and_quality` | `TestSubmitRejectsInvalidSizeAndQuality` | `internal/generator` |
| `GeneratorFlowTests::test_dispatch_task_keeps_task_queued_for_worker_pull` | `TestDispatchKeepsTaskQueuedForWorker` | `internal/generator` |
| `GeneratorFlowTests::test_run_generation_worker_once_pulls_queued_task` | `TestRunWorkerOncePullsQueuedTask` | `internal/generator` |
| `GeneratorFlowTests::test_image_to_image_task_requires_reference_and_returns_reference_payload` | `TestImageToImageRequiresReference` | `internal/generator` |
| `GeneratorFlowTests::test_reference_upload_endpoint_returns_token` | `TestReferenceUploadReturnsToken` | `internal/generator` |
| `GeneratorFlowTests::test_image_to_image_task_accepts_reference_upload_token` | `TestImageToImageAcceptsReferenceToken` | `internal/generator` |
| `GeneratorFlowTests::test_task_list_is_paginated_and_exposes_preview_urls` | `TestTaskListPaginatedWithPreviewURLs` | `internal/generator` |
| `GeneratorFlowTests::test_workspace_history_defers_novel_task_logs` | `TestWorkspaceHistoryDefersNovelTaskLogs` | `internal/generator` |
| `GeneratorFlowTests::test_delete_history_hides_task_and_marks_image_deleted` | `TestDeleteHistoryHidesTaskAndMarksImage` | `internal/generator` |
| `GeneratorFlowTests::test_delete_running_task_cancels_and_hides_task` | `TestDeleteRunningTaskCancelsAndHides` | `internal/generator` |
| `GeneratorFlowTests::test_task_detail_auto_marks_stalled_running_task_failed` | `TestStalledRunningTaskAutoFails` | `internal/generator` |
| `GeneratorFlowTests::test_reconcile_requeues_running_task_with_expired_lease` | `TestReconcileRequeuesExpiredLease` | `internal/generator` |
| `GeneratorFlowTests::test_generate_failure_releases_frozen_quota` | `TestGenerateFailureReleasesFrozenQuota` | `internal/generator` |
| `GeneratorFlowTests::test_failed_task_api_only_exposes_generic_error_to_workspace_user` | `TestFailedTaskHidesProviderError` | `internal/generator` |
| `GeneratorFlowTests::test_retry_happens_inside_same_task_record` | `TestRetryReusesSameTaskRecord` | `internal/generator` |
| `GeneratorFlowTests::test_openai_compatible_channel_can_decode_b64_and_store_image` | `TestOpenAIChannelDecodesB64Image` | `internal/provider` |
| `GeneratorFlowTests::test_openai_compatible_channel_sends_reference_image_for_image_to_image` | `TestOpenAIChannelSendsReferenceImage` | `internal/provider` |
| `GeneratorMigrationTests::test_migration_disables_legacy_yunmeng_channels` | `TestLegacyYunmengChannelsDisabled` | `internal/db` |

`test_workspace_history_defers_novel_task_logs` 断言「查询数不随日志长度增长」，在 Go 中翻译为
「读取历史列表时不加载 `novelviz_novelvisualizationtask.logs` 列」——用列投影断言（SQL 中不出现
`logs`）而不是查询计数，因为 Go 没有等价的 `assertNumQueries`；这是**语义等价改写**，记在
§合理改写 一节。

`GeneratorMigrationTests` 针对 Django 迁移历史。0.0.1 是全新基线（无迁移历史），该测试的**行为
意图**是「历史遗留的云梦渠道不得可用」，翻译为对种子/迁移 SQL 的静态断言 + 运行时渠道判定
（`internal/db`），不保留迁移回放机制。

## 4. apps/mediahub/tests.py（23 → 23）

| Django 测试 | Go 测试 | 目标包 |
| --- | --- | --- |
| `MediaIsolationTests::test_other_user_cannot_fetch_download_url` | `TestOtherUserCannotFetchDownloadURL` | `internal/media` |
| `MediaIsolationTests::test_download_filename_uses_prompt` | `TestDownloadFilenameUsesPrompt` | `internal/media` |
| `MediaIsolationTests::test_download_url_preview_returns_resized_inline_image` | `TestDownloadPreviewResizedInline` | `internal/media` |
| `MediaIsolationTests::test_download_url_preview_uses_configured_max_dimension` | `TestDownloadPreviewUsesConfiguredMaxDimension` | `internal/media` |
| `MediaIsolationTests::test_purge_deleted_image_files_removes_storage_after_retention` | `TestPurgeDeletedImageFilesAfterRetention` | `internal/media` |
| `MediaIsolationTests::test_cleanup_media_storage_returns_category_counts` | `TestCleanupMediaStorageReturnsCounts` | `internal/media` |
| `MediaIsolationTests::test_cleanup_media_storage_command_runs` | `TestCleanupMediaStorageCommandRuns` | `internal/media` |
| `MediaIsolationTests::test_expire_temporary_reference_images_only_removes_expired_uploads` | `TestExpireTemporaryReferenceImages` | `internal/media` |
| `MediaIsolationTests::test_expire_temporary_reference_images_task_returns_deleted_count` | `TestExpireReferenceImagesReturnsCount` | `internal/media` |
| `MediaIsolationTests::test_fetch_remote_image_blocks_private_ip_targets` | `TestFetchRemoteImageBlocksPrivateIP` | `internal/media` |
| `MediaIsolationTests::test_fetch_remote_image_allows_explicitly_configured_host` | `TestFetchRemoteImageAllowsConfiguredHost` | `internal/media` |
| `ResumableUploadTests::test_create_session_and_resume_by_client_upload_id` | `TestCreateSessionAndResume` | `internal/media` |
| `ResumableUploadTests::test_chunk_upload_is_idempotent_and_complete_reference_returns_token` | `TestChunkIdempotentAndCompleteReturnsToken` | `internal/media` |
| `ResumableUploadTests::test_complete_with_missing_chunk_fails` | `TestCompleteWithMissingChunkFails` | `internal/media` |
| `ResumableUploadTests::test_other_user_cannot_access_upload_session` | `TestOtherUserCannotAccessSession` | `internal/media` |
| `ResumableUploadTests::test_oversized_reference_upload_is_rejected` | `TestOversizedReferenceUploadRejected` | `internal/media` |
| `ResumableUploadTests::test_expire_upload_sessions_marks_session_and_removes_chunks` | `TestExpireUploadSessions` | `internal/media` |
| `ResumableUploadTests::test_reference_upload_token_can_create_image_to_image_task` | `TestReferenceTokenCreatesImageToImageTask` | `internal/generator` |
| `ResumableUploadTests::test_epub_upload_completion_creates_book_and_task_can_use_it` | `TestEPUBUploadCreatesBookUsableByTask` | `internal/novel` |
| `ResumableUploadTests::test_novel_task_list_defers_logs` | `TestNovelTaskListDefersLogs` | `internal/novel` |
| `ResumableUploadTests::test_novel_task_event_payload_does_not_nest_logs_inside_task` | `TestNovelEventPayloadNoNestedLogs` | `internal/novel` |
| `ResumableUploadTests::test_novel_task_events_are_limited_and_payload_is_trimmed` | `TestNovelEventsLimitedAndTrimmed` | `internal/novel` |
| `ResumableUploadTests::test_retry_existing_novel_task_reuses_scene_plan` | `TestRetryNovelTaskReusesScenePlan` | `internal/novel` |

`test_cleanup_media_storage_command_runs` 针对 `manage.py cleanup_media_storage` 命令。0.0.1 无
management command 机制，同一入口是 `internal/media.Service.CleanupStorage`，测试改为直接调用该
函数（行为断言完全相同：返回分类计数、删除过期文件）。**合理改写**，非遗漏。

`test_expire_temporary_reference_images_task_returns_deleted_count` 针对 Celery 任务包装器；
0.0.1 无 Celery，同一实现入口是维护循环调用的 `ExpireReferenceImages`。**合理改写**。

## 5. apps/console/tests.py（3 → 3）

| Django 测试 | Go 测试 | 目标包 |
| --- | --- | --- |
| `ConsoleDashboardFailureTests::test_dashboard_recent_failures_reads_failed_tasks_with_raw_detail` | `TestDashboardRecentFailures` | `internal/console` |
| `ConsoleDashboardFailureTests::test_disabled_operator_cannot_access_console_page` | `TestDisabledOperatorCannotAccessPage` | `internal/console` |
| `ConsoleDashboardFailureTests::test_disabled_operator_cannot_access_console_api` | `TestDisabledOperatorCannotAccessAPI` | `internal/console` |

## 6. apps/billing/tests.py（0）

原文件只有一句中文占位注释，无测试。**不翻译**（无内容）。

## 合理改写清单（非遗漏）

以下测试在行为断言不变的前提下改变了**调用方式**，因为 0.0.1 移除了对应的机制：

| Django 机制 | 0.0.1 对应入口 | 受影响测试 |
| --- | --- | --- |
| `assertNumQueries` / `CaptureQueriesContext` | 列投影断言（读路径不得包含重列） | `test_workspace_history_defers_novel_task_logs`、`test_novel_task_list_defers_logs` |
| `manage.py <command>` | 同名 Service 方法 | `test_cleanup_media_storage_command_runs` |
| Celery `delay()` / `apply_async()` | 同进程 worker 的 `Start` + 维护循环 | `test_expire_temporary_reference_images_task_returns_deleted_count` |
| `override_settings` | 构造期注入的 `config.Config` 结构体 | 全部（Go 无全局设置） |
| `django.urls.reverse` | 直接写路径（与前端契约一致，路径本就是冻结契约） | 全部 |
| Django 迁移回放 | 单次基线 schema + 静态断言 | `GeneratorMigrationTests` |
| `patch("...requests.post")` | `httptest.NewServer` 假上游 | `test_chat_uses_selected_llm_channel`、两个 openai 渠道测试 |

## 遗漏清单

| 遗漏项 | 数量 | 理由 |
| --- | --- | --- |
| `InternalReleasePublishAPITests`（2 个测试） | 2 | release-push 接口与 `RELEASE_PUSH_*` 由需求清单明确「不移植」，无对应实现可测。属需求排除，不属翻译遗漏。 |

**翻译率 = 58 / 58 = 100%**（分母已排除需求层面不移植的 2 个测试）。
