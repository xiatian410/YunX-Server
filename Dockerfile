FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
WORKDIR /src/provider
COPY provider/package*.json ./
RUN npm ci --omit=dev

FROM eclipse-temurin:17-jdk-jammy AS provider
WORKDIR /src/provider
COPY provider/ ./
RUN ./gradlew --no-daemon test fatJar

FROM golang:1.26-bookworm AS server
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=0.2.0-draft
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/webassets/dist/ internal/webassets/dist/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/yunx-server ./cmd/yunx-server

FROM node:22-bookworm-slim
ARG VERSION=0.2.0-draft
ARG VCS_REF=unknown
LABEL org.opencontainers.image.title="YunX Server" \
      org.opencontainers.image.description="YunX NAS server with the Android MD3 web interface" \
      org.opencontainers.image.source="https://github.com/xiatian410/YunX-Server" \
      org.opencontainers.image.licenses="AGPL-3.0" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$VCS_REF
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates openjdk-17-jre-headless chromium ffmpeg curl && rm -rf /var/lib/apt/lists/* && mkdir /data /downloads /opt/yunx && chown node:node /data /downloads /opt/yunx
COPY --from=server /out/yunx-server /usr/local/bin/yunx-server
COPY --from=provider /src/provider/build/libs/yunx-provider.jar /opt/yunx/provider.jar
COPY --from=web /src/provider/node_modules /opt/yunx/node_modules
COPY provider/browser.mjs /opt/yunx/browser.mjs
COPY LICENSE NOTICE.md /usr/share/doc/yunx-server/
ENV YUNX_ADDR=:8080 YUNX_DATA_DIR=/data YUNX_DOWNLOAD_DIR=/downloads YUNX_PROVIDER_JAR=/opt/yunx/provider.jar YUNX_BROWSER_SCRIPT=/opt/yunx/browser.mjs YUNX_CHROMIUM=/usr/bin/chromium
USER 1000:1000
EXPOSE 8080
VOLUME ["/data", "/downloads"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD curl -fsS http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/usr/local/bin/yunx-server"]
