#!/usr/bin/env bash
set -euo pipefail

if ! command -v protoc >/dev/null 2>&1; then
  echo "error: protoc not found. Install protobuf compiler first." >&2
  exit 1
fi

if ! command -v protoc-gen-go >/dev/null 2>&1; then
  echo "error: protoc-gen-go not found. Install with: go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2" >&2
  exit 1
fi

if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
  echo "error: protoc-gen-go-grpc not found. Install with: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0" >&2
  exit 1
fi

protoc \
  --go_out=proto/gen --go_opt=paths=source_relative \
  --go-grpc_out=proto/gen --go-grpc_opt=paths=source_relative \
  proto/agent.proto

printf "generated: proto/gen/agent.pb.go and proto/gen/agent_grpc.pb.go\n"
