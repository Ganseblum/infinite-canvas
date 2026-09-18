# 构建阶段：编译静态二进制
FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# 运行阶段：仅保留二进制与 CA 证书，非 root 运行
FROM alpine:3.20

# 预建媒体目录并交给运行用户：local 驱动（STORAGE_DRIVER=local）需要可写，
# 命名卷首次挂载时会继承镜像目录的属主。
# ffmpeg 含 libx264：媒体水印烧录（免费档）与视频水印重编码依赖，缺失时
# WATERMARK_ENABLED=true 的容器无法完成水印任务（fail-closed，不泄漏干净件）。
RUN apk add --no-cache ca-certificates ffmpeg \
    && adduser -D -H -u 10001 appuser \
    && mkdir -p /data/media \
    && chown appuser:appuser /data/media

COPY --from=build /out/server /usr/local/bin/server

USER appuser
EXPOSE 8080
ENTRYPOINT ["server"]
