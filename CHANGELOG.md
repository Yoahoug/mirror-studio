# 更新日志

本文件是所有版本信息的单一来源：应用内的「更新说明」弹窗由服务端在启动时解析本文件生成，
`scripts/release.py` 也依赖本文件判断目标版本是否已归档。

## [Unreleased]

## [0.0.1] - 2026-10-09

### Changed

- Go 重写、单容器、单卷、SQLite；全新基线，不兼容 1.x 数据结构
- 后端由 Django + Postgres + 独立 worker 三服务合并为单进程单二进制
- 前端两个包合并为一个包，构建产物路径保持不变

### Removed

- 移除 MinIO 双存储分支、腾讯云 SES、Postgres 与 Celery 时代遗留脚本
- 移除内部更新推送接口与 RELEASE_PUSH_* 配置
- 移除 AUTO_* 启动开关、GENERATION_TASKS_RUN_INLINE 等运行模式开关
- 移除 django.contrib.admin 与不可达模板
