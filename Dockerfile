FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS server
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/webassets/dist/ internal/webassets/dist/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/yunx-server ./cmd/yunx-server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg curl && rm -rf /var/lib/apt/lists/* && groupadd -g 1000 yunx && useradd -u 1000 -g yunx -M -s /usr/sbin/nologin yunx && mkdir /data /downloads && chown yunx:yunx /data /downloads
COPY --from=server /out/yunx-server /usr/local/bin/yunx-server
COPY LICENSE NOTICE.md /usr/share/doc/yunx-server/
ENV YUNX_ADDR=:8080 YUNX_DATA_DIR=/data YUNX_DOWNLOAD_DIR=/downloads
USER 1000:1000
EXPOSE 8080
VOLUME ["/data", "/downloads"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD curl -fsS http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/usr/local/bin/yunx-server"]
