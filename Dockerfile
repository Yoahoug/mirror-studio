# syntax=docker/dockerfile:1

# ── 段 1/3：前端（两个 SPA + SSR 页面的 CSS）──
# 产物直接写进仓库根的 static/（Go 以 go:embed all:static 打进二进制）。
# 网络受限的环境可用 build-arg 换源（默认走国内镜像，直连可用 --build-arg 覆盖）。
FROM node:20-slim AS frontend-builder
ARG NPM_REGISTRY=https://registry.npmmirror.com
ENV npm_config_registry=${NPM_REGISTRY}
WORKDIR /app
COPY package.json package-lock.json ./
COPY frontend/shared/vite.config.base.ts ./frontend/shared/
COPY frontend/workspace/ ./frontend/workspace/
COPY frontend/console/ ./frontend/console/
COPY frontend/tsconfig.json ./frontend/
COPY static_src/ ./static_src/
COPY tailwind.config.js postcss.config.js ./
RUN npm ci --fetch-retries=5 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000
RUN npm run build

# ── 段 2/3：Go 静态二进制 ──
# 纯 Go SQLite 驱动（modernc.org/sqlite）+ 内嵌 tzdata，CGO_ENABLED=0 即可。
# golang 镜像自 1.24 起不再提供 slim 变体，Alpine 是最小的构建段。
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
ARG GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0 GOFLAGS=-trimpath GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/static ./static
RUN go build -o /out/mirror-studio \
    -ldflags "-s -w -X main.version=$(cat VERSION)" \
    ./.
# 预建数据目录并归属 65532（distroless 的 nonroot）：卷挂载点上应用要能创建
# media/、novel/、novelviz/uploads/ 与 SQLite 的 WAL 伴生文件。
RUN mkdir -p /out/data/media /out/data/novel /out/data/novelviz/uploads \
    && chown -R 65532:65532 /out/data

# ── 段 3/3：运行时 ──
# distroless/static：无 shell、无包管理器，只有 CA 证书与 /nonroot 用户。
# 二进制自带内嵌静态资源、SSR 模板、迁移与时区库（time/tzdata）；运行时还需要
# 可写 /app/data（SQLite、媒体、EPUB）与只读 /app/CHANGELOG.md（「更新说明」数据源）。
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=go-builder --chown=65532:65532 /out/mirror-studio /app/mirror-studio
COPY --from=go-builder --chown=65532:65532 /out/data /app/data
COPY CHANGELOG.md /app/CHANGELOG.md
# 数据（db.sqlite3、media/、novel/、novelviz/uploads/）全部落在一个卷里。
VOLUME ["/app/data"]
ENV APP_LISTEN=0.0.0.0:8000 \
    DATABASE_URL=sqlite:////app/data/db.sqlite3 \
    MEDIA_ROOT=/app/data/media \
    NOVELVIZ_BOOK_DIR=/app/data/novel \
    NOVELVIZ_UPLOAD_DIR=/app/data/novelviz/uploads
EXPOSE 8000
USER nonroot:nonroot
ENTRYPOINT ["/app/mirror-studio"]
