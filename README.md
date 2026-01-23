# Restarter

A Kubernetes controller for monitoring and auto-restarting StatefulSet pods, designed to handle cases where pods stop working after GKE node upgrades.

## Overview

Restarter is a Kubernetes controller built with [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) that watches StatefulSet pods and automatically restarts unhealthy ones. It's particularly useful for legacy applications where advanced liveness probe configurations or custom postStart hooks cannot be implemented.

The controller uses Kubernetes watches and informers to react to pod changes in real-time, making it more efficient than polling-based solutions.

## Features

- **Controller-based architecture**: Uses controller-runtime for efficient event-driven monitoring
- **Real-time monitoring**: Watches pod changes via Kubernetes informers
- **Flexible pod filtering**: Monitor pods by StatefulSet name, label selector, or both
- **Environment variable support**: All configuration options can be set via environment variables
- **Health checks**: Performs health checks based on pod status and optional HTTP endpoints
- **Automatic restart**: Automatically restarts unhealthy pods by deleting them (StatefulSet controller recreates)
- **Namespace-scoped**: Monitors pods in a specific namespace for better isolation
- **Configurable**: Supports custom health check URLs and timeouts

## Usage

### Command Line Options

```bash
restarter [flags]
```

Flags (all can be set via environment variables):
- `--namespace` / `NAMESPACE` (default: "default"): Kubernetes namespace to monitor
- `--statefulset` / `STATEFULSET_NAME` (optional): Name of the StatefulSet to monitor. Either this or `--pod-label-selector` must be provided
- `--pod-label-selector` / `POD_LABEL_SELECTOR` (optional): Pod label selector (e.g., `app=router,component=druid`). Either this or `--statefulset` must be provided
- `--health-check-url` / `HEALTH_CHECK_URL` (optional): HTTP health check URL path (e.g., `/health`)
- `--health-check-timeout` / `HEALTH_CHECK_TIMEOUT` (default: "5s"): Timeout for all health checks
- `--exec-check-command` / `EXEC_CHECK_COMMAND` (optional): Command to execute in container (e.g., `ps aux | grep java`)
- `--exec-check-container` / `EXEC_CHECK_CONTAINER` (optional): Container name for exec check (empty for first container)
- `--exec-check-expected` / `EXEC_CHECK_EXPECTED` (optional): Expected output from exec command (empty to just check exit code)
- `--tcp-check-port` / `TCP_CHECK_PORT` (optional): TCP port to check for connectivity (0 to disable)

**Note**: At least one of `--statefulset` or `--pod-label-selector` must be provided. Both can be used together for more precise filtering.

**Health Check Layers**: The controller performs health checks in multiple layers:
1. **Pod Status**: Checks if pod is Running and Ready
2. **HTTP Check** (if configured): Performs HTTP health check
3. **TCP Check** (if configured): Checks if TCP port is accepting connections
4. **Exec Check** (if configured): Executes a command inside the container to verify application is responding

### Examples

**Monitor by StatefulSet name:**
```bash
restarter --namespace druid --statefulset druid-router
```

**Monitor by pod labels:**
```bash
restarter --namespace druid --pod-label-selector "app=router,component=druid"
```

**Monitor by both StatefulSet and labels (more precise filtering):**
```bash
restarter --namespace druid --statefulset druid-router --pod-label-selector "app=router"
```

**Monitor with HTTP health check:**
```bash
restarter --namespace druid --statefulset druid-router --health-check-url /status/health
```

**Using environment variables:**
```bash
export NAMESPACE=druid
export STATEFULSET_NAME=druid-router
export HEALTH_CHECK_URL=/status/health
export HEALTH_CHECK_TIMEOUT=10s
restarter
```

**Using environment variables with label selector:**
```bash
export NAMESPACE=druid
export POD_LABEL_SELECTOR="app=router,component=druid"
export HEALTH_CHECK_URL=/status/health
restarter
```

**With exec-based health check (detects stuck applications):**
```bash
restarter \
  --namespace druid \
  --statefulset druid-router \
  --exec-check-command "ps aux | grep -v grep | grep java" \
  --exec-check-expected "java"
```

**With multiple health check layers:**
```bash
restarter \
  --namespace druid \
  --statefulset druid-router \
  --health-check-url /status/health \
  --tcp-check-port 8080 \
  --exec-check-command "curl -f http://localhost:8080/status/health || exit 1"
```

## Deployment

### Quick Start

Kubernetes manifests are provided in the `manifests/` directory. See [manifests/README.md](manifests/README.md) for detailed documentation.

**Deploy the controller:**
```bash
# Update the image and configuration in manifests/restarter.yaml first
kubectl apply -f manifests/restarter.yaml
```

**To deploy to a different namespace:**
```bash
# Update namespace in restarter.yaml, then:
kubectl create namespace <your-namespace>  # if it doesn't exist
kubectl apply -f manifests/restarter.yaml
```

### Manifest File

The `manifests/` directory contains:
- `restarter.yaml` - Complete manifest including ServiceAccount, Role, RoleBinding, and Deployment

### Required Permissions

The controller needs the following permissions:
- **Pods**: `get`, `list`, `watch`, `delete` - To monitor and restart pods
- **Pods/exec**: `create` - For exec-based health checks
- **StatefulSets**: `get`, `list` - To filter pods by StatefulSet (when using `STATEFULSET_NAME`)

### Configuration

Update environment variables in `manifests/restarter.yaml`:

- `NAMESPACE` - Target namespace to monitor
- `STATEFULSET_NAME` or `POD_LABEL_SELECTOR` - Pod filtering criteria
- `HEALTH_CHECK_URL` - Optional HTTP health check endpoint
- `HEALTH_CHECK_TIMEOUT` - Health check timeout
- `EXEC_CHECK_COMMAND` - Optional exec command for health checks
- `TCP_CHECK_PORT` - Optional TCP port check
- `HEALTH_PROBE_BIND_ADDRESS` - Health probe bind address (default: ":8080")

### Example Deployment

Here's a minimal example:

1. Create a ServiceAccount with appropriate RBAC permissions:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: restarter
  namespace: druid
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: restarter
  namespace: druid
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "delete"]
- apiGroups: ["apps"]
  resources: ["statefulsets"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: restarter
  namespace: druid
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: restarter
subjects:
- kind: ServiceAccount
  name: restarter
  namespace: druid
```

2. Deploy restarter:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: restarter
  namespace: druid
spec:
  replicas: 1
  selector:
    matchLabels:
      app: restarter
  template:
    metadata:
      labels:
        app: restarter
    spec:
      serviceAccountName: restarter
      containers:
      - name: restarter
        image: your-registry/restarter:latest
        env:
        - name: NAMESPACE
          value: "druid"
        - name: STATEFULSET_NAME
          value: "druid-router"
        - name: HEALTH_CHECK_URL
          value: "/status/health"
        - name: HEALTH_CHECK_TIMEOUT
          value: "5s"
        # Alternative: use pod label selector instead of StatefulSet name
        # - name: POD_LABEL_SELECTOR
        #   value: "app=router,component=druid"
```

For production deployments, use `manifests/restarter.yaml` which includes all required resources.

## Building

**Local build:**
```bash
make build
```

**Docker image:**
```bash
docker build -t restarter:latest .
```

The project includes a GitHub Actions workflow (`.github/workflows/build.yml`) that automatically builds and pushes Docker images to Docker Hub on pushes to `master` branch or when manually triggered.

## Development

### Pre-commit Hooks

This project uses pre-commit hooks to ensure code quality before commits. The hooks automatically format code, run linters, and execute tests.

#### Installation

1. **Install pre-commit:**
   ```bash
   pip install pre-commit
   ```
   or
   ```bash
   brew install pre-commit
   ```

2. **Install the Git hooks:**
   ```bash
   pre-commit install
   ```
   This sets up the necessary Git hook scripts in `.git/hooks` to run the hooks defined in `.pre-commit-config.yaml`.

3. **Run hooks manually on all files:**
   ```bash
   pre-commit run --all-files
   ```

4. **Install additional dependencies (if needed):**
   ```bash
   # golangci-lint (required for linting)
   brew install golangci-lint
   # or download from https://golangci-lint.run/usage/install/
   ```

#### What the hooks do

The pre-commit hooks automatically:
- Format code with `go fmt`
- Run `go vet` for static analysis
- Run `golangci-lint` for comprehensive linting
- Run `go mod tidy` to ensure dependencies are clean
- Run tests with race detector
- Check for common issues (trailing whitespace, large files, merge conflicts, private keys, etc.)

The pre-commit hooks align with CI workflow checks to catch issues locally before committing.

### Manual Development Commands

```bash
# Format code
make fmt

# Run linter
make lint

# Run tests
make test

# Run tests with race detector
make test-race
```
