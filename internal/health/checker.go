package health

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// Checker performs health checks on pods.
type Checker struct {
	httpClient *http.Client
	timeout    time.Duration
	k8sClient  kubernetes.Interface
	restConfig *rest.Config
}

// NewChecker creates a new health checker.
func NewChecker(timeout time.Duration) *Checker {
	return &Checker{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// SetKubernetesClient sets the Kubernetes client for exec-based checks.
func (c *Checker) SetKubernetesClient(client kubernetes.Interface, config *rest.Config) {
	c.k8sClient = client
	c.restConfig = config
}

// HealthCheckOptions contains options for health checking.
type HealthCheckOptions struct {
	HTTPCheckURL   string // HTTP health check URL path (e.g., "/health")
	ExecCommand    string // Command to execute in container (e.g., "ps aux | grep java")
	TCPPort        int    // TCP port to check (0 to disable)
	ContainerName  string // Container name (empty for first container)
	ExpectedOutput string // Expected output from exec command (empty to just check exit code)
}

// IsPodHealthy checks if a pod is healthy based on its status and optional health checks.
func (c *Checker) IsPodHealthy(ctx context.Context, pod *corev1.Pod, opts HealthCheckOptions) (bool, error) {
	// Layer 1: Check pod phase
	if pod.Status.Phase != corev1.PodRunning {
		return false, nil
	}

	// Layer 2: Check if all containers are ready
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			if condition.Status != corev1.ConditionTrue {
				return false, nil
			}
		}
	}

	// Layer 3: HTTP health check (if configured)
	if opts.HTTPCheckURL != "" {
		healthy, err := c.checkHTTPHealth(ctx, pod, opts.HTTPCheckURL)
		if err != nil {
			return false, fmt.Errorf("http health check failed: %w", err)
		}
		if !healthy {
			return false, nil
		}
	}

	// Layer 4: TCP port check (if configured)
	if opts.TCPPort > 0 {
		healthy, err := c.checkTCPPort(ctx, pod, opts.TCPPort)
		if err != nil {
			return false, fmt.Errorf("tcp port check failed: %w", err)
		}
		if !healthy {
			return false, nil
		}
	}

	// Layer 5: Exec command check (if configured)
	if opts.ExecCommand != "" {
		healthy, err := c.checkExecCommand(ctx, pod, opts.ExecCommand, opts.ContainerName, opts.ExpectedOutput)
		if err != nil {
			return false, fmt.Errorf("exec command check failed: %w", err)
		}
		if !healthy {
			return false, nil
		}
	}

	return true, nil
}

// IsPodHealthyLegacy is the legacy method for backward compatibility.
func (c *Checker) IsPodHealthyLegacy(ctx context.Context, pod *corev1.Pod, healthCheckURL string) (bool, error) {
	return c.IsPodHealthy(ctx, pod, HealthCheckOptions{
		HTTPCheckURL: healthCheckURL,
	})
}

// checkHTTPHealth performs an HTTP health check on a pod.
func (c *Checker) checkHTTPHealth(ctx context.Context, pod *corev1.Pod, healthCheckURL string) (bool, error) {
	if pod.Status.PodIP == "" {
		return false, fmt.Errorf("pod IP is not available")
	}

	// healthCheckURL should be a path (e.g., "/health" or "/status/health")
	// Construct full URL with default port 8080
	url := fmt.Sprintf("http://%s:8080%s", pod.Status.PodIP, healthCheckURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, nil // Pod is unhealthy if we can't reach it
	}
	defer resp.Body.Close()

	// Consider 2xx and 3xx status codes as healthy
	return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
}

// checkTCPPort checks if a TCP port is accepting connections.
func (c *Checker) checkTCPPort(ctx context.Context, pod *corev1.Pod, port int) (bool, error) {
	if pod.Status.PodIP == "" {
		return false, fmt.Errorf("pod IP is not available")
	}

	address := fmt.Sprintf("%s:%d", pod.Status.PodIP, port)

	dialer := net.Dialer{
		Timeout: c.timeout,
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false, nil // Port is not accepting connections
	}
	defer conn.Close()

	return true, nil
}

// checkExecCommand executes a command in the pod container and checks the result.
func (c *Checker) checkExecCommand(ctx context.Context, pod *corev1.Pod, command, containerName, expectedOutput string) (bool, error) {
	if c.k8sClient == nil || c.restConfig == nil {
		return false, fmt.Errorf("kubernetes client not configured for exec checks")
	}

	if len(pod.Spec.Containers) == 0 {
		return false, fmt.Errorf("pod has no containers")
	}

	// Determine container name
	container := containerName
	if container == "" {
		container = pod.Spec.Containers[0].Name
	}

	// Create exec request
	req := c.k8sClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		Timeout(c.timeout)

	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   []string{"sh", "-c", command},
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return false, fmt.Errorf("failed to create executor: %w", err)
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	// Check if command executed successfully
	if err != nil {
		return false, nil // Command failed or timed out
	}

	// If expected output is specified, check if it matches
	if expectedOutput != "" {
		output := stdout.String()
		if output != expectedOutput && !contains(output, expectedOutput) {
			return false, nil // Output doesn't match expected
		}
	}

	return true, nil
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// CheckPodStatus checks if a pod has problematic status conditions.
func (c *Checker) CheckPodStatus(pod *corev1.Pod) bool {
	// Check for common problematic conditions
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			if condition.Status != corev1.ConditionTrue {
				return false
			}
		}
		if condition.Type == corev1.PodScheduled {
			if condition.Status != corev1.ConditionTrue {
				return false
			}
		}
	}

	// Check container statuses
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if !containerStatus.Ready {
			return false
		}
		if containerStatus.State.Waiting != nil {
			// Pod is waiting (possibly stuck)
			return false
		}
	}

	return true
}
