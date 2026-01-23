# Restarter Kubernetes Manifests

This directory contains a consolidated Kubernetes manifest for deploying the restarter controller.

## File

- `restarter.yaml` - Complete manifest including ServiceAccount, Role, RoleBinding, and Deployment

## Quick Start

1. **Update the manifest** with your configuration:
   - Set `STATEFULSET_NAME` or `POD_LABEL_SELECTOR` environment variable
   - Update `NAMESPACE` to match your target namespace
   - Update the image reference
   - Update namespace in all resources (default is `default`)

2. **Apply the manifest:**
   ```bash
   kubectl apply -f manifests/restarter.yaml
   ```

3. **To deploy to a different namespace:**
   ```bash
   # Update namespace in restarter.yaml, then:
   kubectl create namespace <your-namespace>  # if it doesn't exist
   kubectl apply -f manifests/restarter.yaml
   ```

## Configuration

### Environment Variables

The deployment uses environment variables for configuration. Update them in `restarter.yaml`:

- **NAMESPACE** (required): Target namespace to monitor
- **STATEFULSET_NAME** (optional): StatefulSet name to monitor
- **POD_LABEL_SELECTOR** (optional): Pod label selector (e.g., `app=router,component=druid`)
- **HEALTH_CHECK_URL** (optional): HTTP health check endpoint path
- **HEALTH_CHECK_TIMEOUT** (optional): Health check timeout (default: 5s)
- **EXEC_CHECK_COMMAND** (optional): Command to execute in container for health check
- **EXEC_CHECK_CONTAINER** (optional): Container name for exec check (empty for first container)
- **EXEC_CHECK_EXPECTED** (optional): Expected output from exec command
- **TCP_CHECK_PORT** (optional): TCP port to check for connectivity
- **HEALTH_PROBE_BIND_ADDRESS** (default: ":8080"): Bind address for health probes

**Note**: Either `STATEFULSET_NAME` or `POD_LABEL_SELECTOR` must be set.

### Example Configurations

**Monitor by StatefulSet name:**
```yaml
env:
  - name: NAMESPACE
    value: "druid"
  - name: STATEFULSET_NAME
    value: "druid-router"
  - name: HEALTH_CHECK_URL
    value: "/status/health"
```

**Monitor by pod labels:**
```yaml
env:
  - name: NAMESPACE
    value: "druid"
  - name: POD_LABEL_SELECTOR
    value: "app=router,component=druid"
  - name: HEALTH_CHECK_URL
    value: "/status/health"
```

**With exec-based health check (detects stuck applications):**
```yaml
env:
  - name: NAMESPACE
    value: "druid"
  - name: STATEFULSET_NAME
    value: "druid-router"
  - name: EXEC_CHECK_COMMAND
    value: "ps aux | grep -v grep | grep java"
  - name: EXEC_CHECK_EXPECTED
    value: "java"
```

**Multiple health check layers:**
```yaml
env:
  - name: NAMESPACE
    value: "druid"
  - name: STATEFULSET_NAME
    value: "druid-router"
  - name: HEALTH_CHECK_URL
    value: "/status/health"
  - name: TCP_CHECK_PORT
    value: "8080"
  - name: EXEC_CHECK_COMMAND
    value: "curl -f http://localhost:8080/status/health || exit 1"
```

## Permissions

The Role includes the minimum required permissions:
- **Pods**: `get`, `list`, `watch`, `delete` - Required to monitor and restart pods
- **Pods/exec**: `create` - Required for exec-based health checks
- **StatefulSets**: `get`, `list` - Required when filtering by StatefulSet name

## Security

The deployment includes:
- Non-root user (UID 65534)
- Resource limits
- ServiceAccount with least-privilege Role

## Health Probes

The deployment includes health probes that use the controller-runtime manager's built-in `/healthz` and `/readyz` endpoints. These are enabled by default via the `HEALTH_PROBE_BIND_ADDRESS` environment variable set to `:8080`.

The controller-runtime manager automatically provides these endpoints when `HealthProbeBindAddress` is set.

## Troubleshooting

**Check controller logs:**
```bash
kubectl logs -f deployment/restarter -n <namespace>
```

**Check RBAC permissions:**
```bash
kubectl auth can-i delete pods --as=system:serviceaccount:<namespace>:restarter -n <namespace>
kubectl auth can-i create pods/exec --as=system:serviceaccount:<namespace>:restarter -n <namespace>
```

**Verify controller is running:**
```bash
kubectl get deployment restarter -n <namespace>
kubectl get pods -l app=restarter -n <namespace>
```

**Check pod health:**
```bash
kubectl get pods -n <namespace>
kubectl describe pod <pod-name> -n <namespace>
```
