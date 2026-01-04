# Docker Compose Setup

This directory contains Docker Compose configurations for running the simulation-core in containerized environments.

## Files

- **docker-compose.yml**: Main compose file for production-like setup
- **docker-compose.dev.yml**: Development overrides (mounts source, debug logging)
- **README.md**: This file

## Quick Start

### Single Instance (Development)

```bash
# From the project root
cd deployments/docker

# Build and start a single simulator instance
docker-compose up --build

# Or in detached mode
docker-compose up -d

# View logs
docker-compose logs -f simd

# Stop
docker-compose down
```

The simulator will be available at:
- **gRPC**: `localhost:50051`
- **HTTP**: `http://localhost:8080`

### Multiple Instances (Testing Concurrent Runs)

```bash
# Start 3 simulator instances
docker-compose up --scale simd-instance=3

# Access instances via service names:
# - simd-instance_1:50051 (gRPC), simd-instance_1:8080 (HTTP)
# - simd-instance_2:50051 (gRPC), simd-instance_2:8080 (HTTP)
# - simd-instance_3:50051 (gRPC), simd-instance_3:8080 (HTTP)
```

### Development Mode

```bash
# Use development overrides (debug logging, source mounting)
docker-compose -f docker-compose.yml -f docker-compose.dev.yml up
```

## Service Configuration

### Ports

- **50051**: gRPC server (for CLI and programmatic access)
- **8080**: HTTP server (for REST API and metrics streaming)

### Environment Variables

The simulator accepts command-line flags. You can override them in `docker-compose.yml`:

```yaml
services:
  simd:
    command:
      - "-grpc-addr"
      - ":50051"
      - "-http-addr"
      - ":8080"
      - "-log-level"
      - "debug"  # Options: debug, info, warn, error
```

### Networking

All services run on the `simulation-network` bridge network, allowing:
- Service discovery via Docker DNS (e.g., `simd-instance_1:8080`)
- Isolated network for simulator instances
- Backend can connect to instances via service names

## Use Cases

### 1. Local Development

Run a single instance for local testing:

```bash
docker-compose up
```

### 2. Testing Concurrent Runs

Test how the backend handles multiple simulator instances:

```bash
docker-compose up --scale simd-instance=5
```

### 3. Backend Integration

The backend can deploy containers dynamically using Docker API:

```go
// Example: Deploy a new simulator instance per simulation run
containerConfig := &container.Config{
    Image: "simulation-core:latest",
    Cmd: []string{
        "-grpc-addr", ":50051",
        "-http-addr", ":8080",
        "-log-level", "info",
    },
}
hostConfig := &container.HostConfig{
    NetworkMode: "simulation-network",
}
```

### 4. Production Deployment

For production, use Kubernetes or Docker Swarm instead of Docker Compose. This setup is intended for local development and testing.

## Health Checks

The compose file includes health checks that verify the HTTP server is responding. Health check status can be viewed with:

```bash
docker-compose ps
```

## Troubleshooting

### Port Already in Use

If ports 50051 or 8080 are already in use, modify the port mappings in `docker-compose.yml`:

```yaml
ports:
  - "50052:50051"  # Map host port 50052 to container port 50051
  - "8081:8080"    # Map host port 8081 to container port 8080
```

### View Logs

```bash
# All services
docker-compose logs

# Specific service
docker-compose logs simd

# Follow logs
docker-compose logs -f simd
```

### Clean Up

```bash
# Stop and remove containers
docker-compose down

# Remove containers, networks, and volumes
docker-compose down -v

# Remove images
docker-compose down --rmi all
```

## Network Access

Services can communicate with each other using Docker service names:

```bash
# From within the network, access simulator via:
curl http://simd:8080/v1/runs

# Or for scaled instances:
curl http://simd-instance_1:8080/v1/runs
curl http://simd-instance_2:8080/v1/runs
```

## Next Steps

- **Kubernetes**: See `deployments/k8s/` for Kubernetes manifests
- **Production**: Consider using container orchestration platforms for production deployments
- **Monitoring**: Add Prometheus/Grafana services to the compose file for metrics collection

