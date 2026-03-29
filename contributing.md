# Contributing to tidefly-agent

## Prerequisites

- Go 1.26+
- Docker or Podman
- [Task](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`)
- A running Tidefly Plane instance (for end-to-end testing)

## Setup

```bash
git clone https://github.com/tidefly-oss/tidefly-agent
cd tidefly-agent
go mod download
cp .env.example .env
# Fill in PLANE_ENDPOINT and PLANE_TOKEN
```

## Running locally

```bash
task run       # run with .env
task dev       # run with debug logging
task lint      # golangci-lint
task test      # go test ./...
```

## Proto changes

If you change `agent.proto`, regenerate the Go code:

```bash
# Install protoc + plugins first
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

bash scripts/gen-proto.sh
```

The generated files must be committed.

## Pull Requests

- Branch from `develop`, target `develop`
- Keep commits atomic — one logical change per commit
- Use conventional commits: `fix:`, `feat:`, `chore:`, `docs:`
- Run `task lint` before pushing

## Release

Releases are created by pushing a semver tag to `main`:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions builds the multi-arch Docker image and pushes to GHCR automatically.