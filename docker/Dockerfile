# =============================================================================
# Build Stage
# =============================================================================
FROM golang:1.26-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN --mount=type=cache,id=gomod-agent,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN --mount=type=cache,id=gomod-agent,target=/go/pkg/mod \
    --mount=type=cache,id=gobuild-agent,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/tidefly-oss/tidefly-agent/internal/version.Version=${VERSION} \
        -X github.com/tidefly-oss/tidefly-agent/internal/version.Commit=${COMMIT} \
        -X github.com/tidefly-oss/tidefly-agent/internal/version.BuildDate=${BUILD_DATE}" \
      -o /out/tidefly-agent \
      ./cmd/tidefly-agent

# =============================================================================
# Runtime Stage — scratch
# =============================================================================
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/tidefly-agent /tidefly-agent

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="tidefly-agent" \
      org.opencontainers.image.description="Tidefly Agent — worker node daemon" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://github.com/tidefly-oss/tidefly-agent" \
      org.opencontainers.image.licenses="AGPL-3.0"

VOLUME ["/etc/tidefly-agent"]

ENTRYPOINT ["/tidefly-agent"]