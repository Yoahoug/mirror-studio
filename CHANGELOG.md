# 更新日志

本文件是所有版本信息的单一来源：应用内的「更新说明」弹窗由服务端在启动时解析本文件生成，
`scripts/release.py` 也依赖本文件判断目标版本是否已归档。

## [Unreleased]

### Added

- 本地开发者模式 `npm run local`：无需数据库、管理员、登录与注册，固定本地账号自动登录，
  只暴露工作台与管理台；未配置渠道时由本地合成占位图与本地模拟回复，
  生成结果落在 `local-data/`（详见 `docs/LOCAL-MODE.md`）
- 本地开发者模式只占用一个端口（默认 8000）：工作台在 `/`、管理台在 `/admin`，
  Vite 以 middleware 方式挂在同一个 HTTP 服务上（含 HMR），不再需要 3000/3001
- GitHub Actions 工作流 `.github/workflows/container.yml`：推送到 `main`、推送 `v*`
  标签或手动触发时构建容器镜像并发布到 `ghcr.io/yoahoug/mirror-studio`
  （标签跟随 `VERSION`，当前 `0.0.1`）；构建使用 Actions 构建缓存
  （`type=gha, mode=max`），后续构建复用 npm/Go 依赖层

### Changed

- 服务端不再把页面指向 Vite 开发服务器：Go 始终加载 `go:embed` 打包的产物，
  `/workspace`、`/console` 与接口同处一个端口，容器只需暴露一个端口；
  改前端后用 `npm run build:workspace` / `build:console` 重建并重启，
  需要 HMR 时用单端口的 `npm run local` 或按需单独起 Vite 开发服务器
- `docker-compose.yml` 默认使用 GHCR 上发布的镜像（`IMAGE_TAG` 可覆盖），
  同时保留 `docker compose up -d --build` 就地构建
- 新增 `.gitattributes`：`internal/web/testdata/` 参照文件固定为 LF，
  Windows 检出（`core.autocrlf=true`）不再把 golden 转成 CRLF 导致测试失败

### Removed

- `WORKSPACE_VITE_DEV_URL` / `CONSOLE_VITE_DEV_URL` 以及 DEBUG 模式下的页面注入分支
  （连同对应的参照测试与 3 个 golden 文件）；环境里仍残留这两个变量会拒绝启动

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
