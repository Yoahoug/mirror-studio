-- 0.0.1 initial schema.
--
-- Table and column names deliberately match the Django 1.x layout for every
-- table that carries business data, because the differential harness compares
-- `SELECT *` snapshots of those tables between the two implementations.
-- Indexes are not compared, so additional indexes for hot paths are listed at
-- the end of this file.
--
-- 0.x makes no cross-version data-compatibility promise: this is a fresh
-- baseline, and a 1.x database is not migratable to it.

CREATE TABLE "users_user" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "password" varchar(128) NOT NULL,
  "last_login" datetime NULL,
  "is_superuser" bool NOT NULL,
  "username" varchar(150) NOT NULL UNIQUE,
  "first_name" varchar(150) NOT NULL,
  "last_name" varchar(150) NOT NULL,
  "is_staff" bool NOT NULL,
  "is_active" bool NOT NULL,
  "date_joined" datetime NOT NULL,
  "email" varchar(254) NULL UNIQUE,
  "mobile" varchar(20) NULL UNIQUE,
  "role" varchar(16) NOT NULL,
  "status" varchar(16) NOT NULL,
  "last_login_ip" char(39) NULL,
  "last_login_at" datetime NULL
);

CREATE TABLE "users_userprofile" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "nickname" varchar(64) NOT NULL,
  "avatar" varchar(200) NOT NULL,
  "register_ip" char(39) NULL,
  "register_source" varchar(64) NOT NULL,
  "remark" text NOT NULL,
  "user_id" bigint NOT NULL UNIQUE REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE "core_siteconfig" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "key" varchar(128) NOT NULL UNIQUE,
  "value_encrypted" text NOT NULL,
  "is_secret" bool NOT NULL,
  "description" varchar(255) NOT NULL,
  "category" varchar(32) NOT NULL
);

CREATE TABLE "billing_quotaaccount" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "daily_quota" integer NOT NULL CHECK ("daily_quota" >= 0),
  "available_quota" integer NOT NULL,
  "frozen_quota" integer NOT NULL,
  "total_generated_count" integer NOT NULL CHECK ("total_generated_count" >= 0),
  "user_id" bigint NOT NULL UNIQUE REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED,
  "last_reset_date" date NOT NULL
);

CREATE TABLE "billing_quotaledger" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "ledger_no" varchar(32) NOT NULL UNIQUE,
  "change_type" varchar(32) NOT NULL,
  "direction" varchar(8) NOT NULL,
  "amount" integer NOT NULL,
  "before_available" integer NOT NULL,
  "after_available" integer NOT NULL,
  "before_frozen" integer NOT NULL,
  "after_frozen" integer NOT NULL,
  "related_task_no" varchar(32) NOT NULL,
  "remark" varchar(255) NOT NULL,
  "user_id" bigint NOT NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE "generator_imagechannel" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "name" varchar(64) NOT NULL,
  "channel_code" varchar(32) NOT NULL UNIQUE,
  "base_url" varchar(200) NOT NULL,
  "api_path" varchar(128) NOT NULL,
  "api_key_encrypted" text NOT NULL,
  "model_name" varchar(64) NOT NULL,
  "timeout_seconds" integer NOT NULL CHECK ("timeout_seconds" >= 0),
  "max_retries" integer NOT NULL CHECK ("max_retries" >= 0),
  "is_active" bool NOT NULL,
  "priority" integer NOT NULL CHECK ("priority" >= 0),
  "channel_type" varchar(16) NOT NULL,
  "provider_name" varchar(64) NOT NULL,
  "edit_api_path" varchar(128) NOT NULL,
  "provider_vendor" varchar(24) NOT NULL
);

CREATE TABLE "generator_generationtopic" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "title" varchar(128) NOT NULL,
  "cover_image_id" bigint NULL,
  "last_task_at" datetime NOT NULL,
  "is_archived" bool NOT NULL,
  "user_id" bigint NOT NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE "generator_generatetask" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "task_no" varchar(32) NOT NULL UNIQUE,
  "status" varchar(16) NOT NULL,
  "prompt" text NOT NULL,
  "negative_prompt" text NOT NULL,
  "style_code" varchar(32) NOT NULL,
  "size_code" varchar(32) NOT NULL,
  "quota_cost" integer NOT NULL CHECK ("quota_cost" >= 0),
  "provider_request_id" varchar(128) NOT NULL,
  "provider_response_json" text NOT NULL CHECK ((JSON_VALID("provider_response_json") OR "provider_response_json" IS NULL)),
  "error_code" varchar(64) NOT NULL,
  "error_message" varchar(255) NOT NULL,
  "submitted_at" datetime NOT NULL,
  "started_at" datetime NULL,
  "finished_at" datetime NULL,
  "client_request_id" varchar(64) NOT NULL,
  "client_ip" char(39) NULL,
  "quota_finalized" bool NOT NULL,
  "user_id" bigint NOT NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED,
  "topic_id" bigint NULL REFERENCES "generator_generationtopic" ("id") DEFERRABLE INITIALLY DEFERRED,
  "channel_id" bigint NOT NULL REFERENCES "generator_imagechannel" ("id") DEFERRABLE INITIALLY DEFERRED,
  "progress_percent" smallint NOT NULL CHECK ("progress_percent" >= 0),
  "deleted_at" datetime NULL,
  "is_deleted" bool NOT NULL,
  "generation_mode" varchar(24) NOT NULL,
  "reference_image_data_url" text NOT NULL,
  "reference_image_name" varchar(255) NOT NULL,
  "reference_storage_provider" varchar(32) NOT NULL,
  "reference_bucket_name" varchar(128) NOT NULL,
  "reference_object_key" varchar(255) NOT NULL,
  "reference_image_mime_type" varchar(64) NOT NULL,
  "reference_image_size" integer NOT NULL CHECK ("reference_image_size" >= 0),
  "reference_image_width" integer NOT NULL CHECK ("reference_image_width" >= 0),
  "reference_image_height" integer NOT NULL CHECK ("reference_image_height" >= 0),
  "reference_image_sha256" varchar(64) NOT NULL,
  "execution_attempts" integer NOT NULL CHECK ("execution_attempts" >= 0),
  "lease_expires_at" datetime NULL,
  "quality_code" varchar(16) NOT NULL
);

CREATE UNIQUE INDEX "unique_nonblank_client_request_per_user"
  ON "generator_generatetask" ("user_id", "client_request_id")
  WHERE NOT ("client_request_id" = '');

CREATE TABLE "mediahub_generatedimage" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "image_index" integer NOT NULL CHECK ("image_index" >= 0),
  "storage_provider" varchar(32) NOT NULL,
  "bucket_name" varchar(128) NOT NULL,
  "object_key" varchar(255) NOT NULL,
  "mime_type" varchar(64) NOT NULL,
  "file_size" integer NOT NULL CHECK ("file_size" >= 0),
  "width" integer NOT NULL CHECK ("width" >= 0),
  "height" integer NOT NULL CHECK ("height" >= 0),
  "sha256" varchar(64) NOT NULL,
  "status" varchar(16) NOT NULL,
  "expires_at" datetime NOT NULL,
  "deleted_at" datetime NULL,
  "task_id" bigint NOT NULL REFERENCES "generator_generatetask" ("id") DEFERRABLE INITIALLY DEFERRED,
  "user_id" bigint NOT NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED,
  "preview_object_key" varchar(255) NOT NULL
);

CREATE TABLE "mediahub_uploadsession" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "upload_id" varchar(32) NOT NULL UNIQUE,
  "client_upload_id" varchar(128) NOT NULL,
  "kind" varchar(48) NOT NULL,
  "filename" varchar(255) NOT NULL,
  "content_type" varchar(128) NOT NULL,
  "total_size" bigint NOT NULL CHECK ("total_size" >= 0),
  "chunk_size" integer NOT NULL CHECK ("chunk_size" >= 0),
  "total_chunks" integer NOT NULL CHECK ("total_chunks" >= 0),
  "uploaded_chunks" text NOT NULL CHECK ((JSON_VALID("uploaded_chunks") OR "uploaded_chunks" IS NULL)),
  "status" varchar(16) NOT NULL,
  "result_json" text NOT NULL CHECK ((JSON_VALID("result_json") OR "result_json" IS NULL)),
  "expires_at" datetime NOT NULL,
  "completed_at" datetime NULL,
  "user_id" bigint NOT NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE "novelviz_noveluploadedbook" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "display_name" varchar(255) NOT NULL,
  "storage_path" varchar(500) NOT NULL,
  "file_size" integer NOT NULL CHECK ("file_size" >= 0),
  "sha256" varchar(64) NOT NULL,
  "user_id" bigint NOT NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE "novelviz_novelvisualizationtask" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "book_filename" varchar(255) NOT NULL,
  "status" varchar(16) NOT NULL,
  "progress_percent" smallint NOT NULL CHECK ("progress_percent" >= 0),
  "logs" text NOT NULL CHECK ((JSON_VALID("logs") OR "logs" IS NULL)),
  "scenes_json" text NOT NULL CHECK ((JSON_VALID("scenes_json") OR "scenes_json" IS NULL)),
  "error_message" varchar(255) NOT NULL,
  "started_at" datetime NULL,
  "finished_at" datetime NULL,
  "last_event_id" integer NOT NULL CHECK ("last_event_id" >= 0),
  "user_id" bigint NOT NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED,
  "llm_channel_code" varchar(64) NOT NULL,
  "book_source" varchar(16) NOT NULL,
  "uploaded_book_id" bigint NULL REFERENCES "novelviz_noveluploadedbook" ("id") DEFERRABLE INITIALLY DEFERRED,
  "scene_mode" varchar(16) NOT NULL,
  "target_plot" text NOT NULL
);

CREATE TABLE "novelviz_novelsceneimage" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "scene_number" smallint NOT NULL CHECK ("scene_number" >= 0),
  "title" varchar(160) NOT NULL,
  "chapter" varchar(160) NOT NULL,
  "story_significance" text NOT NULL,
  "excerpt" text NOT NULL,
  "visual_prompt" text NOT NULL,
  "status" varchar(16) NOT NULL,
  "error_message" varchar(255) NOT NULL,
  "image_path" varchar(500) NOT NULL,
  "image_url" varchar(600) NOT NULL,
  "attempts" smallint NOT NULL CHECK ("attempts" >= 0),
  "finished_at" datetime NULL,
  "task_id" bigint NOT NULL REFERENCES "novelviz_novelvisualizationtask" ("id") DEFERRABLE INITIALLY DEFERRED,
  "preview_image_path" varchar(500) NOT NULL,
  CONSTRAINT "unique_novel_scene_number_per_task" UNIQUE ("task_id", "scene_number")
);

CREATE TABLE "risk_sensitivewordrule" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "keyword" varchar(100) NOT NULL UNIQUE,
  "match_mode" varchar(16) NOT NULL,
  "is_active" bool NOT NULL,
  "remark" varchar(255) NOT NULL
);

CREATE TABLE "risk_ipaccessrule" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "ip_address" char(39) NOT NULL UNIQUE,
  "is_blocked" bool NOT NULL,
  "reason" varchar(255) NOT NULL,
  "expires_at" datetime NULL
);

CREATE TABLE "risk_riskevent" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "event_type" varchar(64) NOT NULL,
  "request_ip" char(39) NULL,
  "detail_json" text NOT NULL CHECK ((JSON_VALID("detail_json") OR "detail_json" IS NULL)),
  "user_id" bigint NULL REFERENCES "users_user" ("id") DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE "audit_callbackeventlog" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL,
  "updated_at" datetime NOT NULL,
  "event_type" varchar(64) NOT NULL,
  "source" varchar(64) NOT NULL,
  "payload_json" text NOT NULL CHECK ((JSON_VALID("payload_json") OR "payload_json" IS NULL)),
  "success" bool NOT NULL,
  "remark" varchar(255) NOT NULL
);

-- ── service-owned tables (not part of the compared business surface) ──

CREATE TABLE "app_session" (
  "session_key" varchar(64) NOT NULL PRIMARY KEY,
  "data" text NOT NULL,
  "expires_at" datetime NOT NULL
);

-- ── indexes for hot paths that the 1.x schema scanned linearly ──

-- Generation worker claim query: queue status plus lease expiry.
CREATE INDEX "generator_task_claim_idx"
  ON "generator_generatetask" ("status", "lease_expires_at");
-- Task list and per-user pending counts.
CREATE INDEX "generator_task_user_created_idx"
  ON "generator_generatetask" ("user_id", "created_at" DESC);
CREATE INDEX "generator_task_channel_idx"
  ON "generator_generatetask" ("channel_id");
CREATE INDEX "generator_task_topic_idx"
  ON "generator_generatetask" ("topic_id");

CREATE INDEX "generator_topic_user_last_idx"
  ON "generator_generationtopic" ("user_id", "last_task_at" DESC);
CREATE INDEX "generator_topic_user_archived_idx"
  ON "generator_generationtopic" ("user_id", "is_archived");

CREATE INDEX "billing_ledger_user_created_idx"
  ON "billing_quotaledger" ("user_id", "created_at" DESC);

CREATE INDEX "mediahub_image_user_created_idx"
  ON "mediahub_generatedimage" ("user_id", "created_at" DESC);
CREATE INDEX "mediahub_image_status_deleted_idx"
  ON "mediahub_generatedimage" ("status", "deleted_at");
CREATE INDEX "mediahub_image_task_idx"
  ON "mediahub_generatedimage" ("task_id");

CREATE INDEX "mediahub_upload_status_expires_idx"
  ON "mediahub_uploadsession" ("status", "expires_at");
CREATE INDEX "mediahub_upload_user_kind_client_idx"
  ON "mediahub_uploadsession" ("user_id", "kind", "client_upload_id");

CREATE INDEX "novelviz_task_user_created_idx"
  ON "novelviz_novelvisualizationtask" ("user_id", "created_at" DESC);
CREATE INDEX "novelviz_scene_task_number_idx"
  ON "novelviz_novelsceneimage" ("task_id", "scene_number");
CREATE INDEX "novelviz_book_user_created_idx"
  ON "novelviz_noveluploadedbook" ("user_id", "created_at" DESC);

-- Callback event log: the console failure list orders and filters by these.
CREATE INDEX "audit_callback_created_idx"
  ON "audit_callbackeventlog" ("created_at" DESC);
CREATE INDEX "audit_callback_type_created_idx"
  ON "audit_callbackeventlog" ("event_type", "created_at" DESC);

CREATE INDEX "app_session_expires_idx" ON "app_session" ("expires_at");
