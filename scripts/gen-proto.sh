#!/usr/bin/env bash
set -euo pipefail

# Generate Go code from protobuf definitions using buf.
#
# Prereqs:
#   - Go toolchain installed
#   - buf installed: https://buf.build/docs/installation
#
# Output:
#   - Generated code under ./gen/go (gitignored by default)

# Check if buf is installed
if ! command -v buf &> /dev/null; then
    echo "buf is not installed. Installing buf..."
    go install github.com/bufbuild/buf/cmd/buf@v1.62.1
    export PATH="$(go env GOPATH)/bin:$PATH"
fi

# Install required protoc plugins
echo "Installing protoc plugins..."
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

echo "Generating protobuf code..."
PATH="$(go env GOPATH)/bin:$PATH" buf generate

echo "✓ Protobuf generation complete"

