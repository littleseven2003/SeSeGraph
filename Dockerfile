# SeSeGraph 运行镜像：前端构建 → 静态编译 → Alpine 极简运行层
# 构建：docker build -t sesegraph:local .
# 运行：docker run -d --name sesegraph -p 8080:8080 -v ./data:/app/data sesegraph:local

# 阶段一：构建前端静态资源
FROM node:22-alpine AS web
WORKDIR /app/frontend
RUN npm install -g pnpm@9
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

# 阶段二：静态编译后端并嵌入前端产物
FROM golang:1.22-alpine AS build
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /app/frontend/dist/ ./internal/web/assets/
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/sesegraph ./cmd/server

# 阶段三：运行层
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 1000 sesegraph \
    && adduser -S -u 1000 -G sesegraph -h /app sesegraph \
    && mkdir -p /app/data \
    && chown -R sesegraph:sesegraph /app
WORKDIR /app
COPY --from=build /out/sesegraph /app/sesegraph
USER sesegraph
ENV LISTEN_ADDR=:8080 \
    DATA_DIR=/app/data
EXPOSE 8080
VOLUME ["/app/data"]
# 健康检查只访问本机接口，不携带任何凭据。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["/app/sesegraph"]
