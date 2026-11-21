# ---- build: static Go binary ----
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X pahe-arr/internal/buildinfo.Version=$VERSION -X pahe-arr/internal/buildinfo.Commit=$REVISION" -o /out/pahe ./cmd/pahe

# ---- runtime: alpine + ffmpeg ----
FROM alpine:3.24
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/barestu/pahe-arr" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION
RUN apk add --no-cache ffmpeg ca-certificates tzdata su-exec
COPY --from=build /out/pahe /usr/local/bin/pahe
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENV PAHE_CONFIG_DIR=/config PAHE_LISTEN=:8585
EXPOSE 8585
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -qO- http://127.0.0.1:8585/healthz >/dev/null || exit 1
ENTRYPOINT ["/entrypoint.sh"]
CMD ["serve"]
