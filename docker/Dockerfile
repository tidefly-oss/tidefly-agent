FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY ../go.mod go.sum ./
RUN go mod download
COPY .. .

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} CGO_ENABLED=0 \
    go build -ldflags "-X main.version=${VERSION} -s -w" \
    -o tidefly-agent ./cmd/tidefly-agent

# ── Runtime ───────────────────────────────────────────────────────────────────
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/tidefly-agent /tidefly-agent

VOLUME ["/etc/tidefly-agent"]

ENTRYPOINT ["/tidefly-agent"]