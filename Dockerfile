# Multi-stage Dockerfile for simulation-core
# Stage 1: Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
# CGO_ENABLED=0 for static binary, -ldflags for smaller size
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o /build/bin/simd \
    ./cmd/simd

# Stage 2: Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the binary from builder
COPY --from=builder /build/bin/simd /usr/local/bin/simd

# Expose ports
# 50051: gRPC server
# 8080: HTTP server
EXPOSE 50051 8080

# Use non-root user (provided by distroless)
USER nonroot:nonroot

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/simd"]

# Default command (can be overridden)
CMD ["-grpc-addr", ":50051", "-http-addr", ":8080"]

