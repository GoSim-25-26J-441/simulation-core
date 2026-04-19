$ErrorActionPreference = "Stop"

# Generate Go code from protobuf definitions using buf.
#
# Prereqs:
#   - Go toolchain installed
#   - buf installed: https://buf.build/docs/installation
#
# Output:
#   - Generated code under ./gen/go (committed so downstream `go get` works)

# Check if buf is installed
if (-not (Get-Command buf -ErrorAction SilentlyContinue)) {
    Write-Host "buf is not installed. Installing buf..." -ForegroundColor Yellow
    go install github.com/bufbuild/buf/cmd/buf@v1.62.1
}

Write-Host "Installing protoc plugins..." -ForegroundColor Cyan
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

Write-Host "Generating protobuf code..." -ForegroundColor Cyan
$env:PATH = "$(go env GOPATH)/bin" + [IO.Path]::PathSeparator + $env:PATH
buf generate

Write-Host "✓ Protobuf generation complete" -ForegroundColor Green

