package health

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

const defaultHTTPCheckPort = 8080

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
	HTTPCheckPort  int    // HTTP health check port (0 defaults to 8080)
	ExecCommand    string // Command to execute in container (e.g., "ps aux | grep java")
	TCPPort        int    // TCP port to check (0 to disable)
	ContainerName  string // Container name (empty for first container)
	ExpectedOutput string // Expected output from exec command (empty to just check exit code)
}

// IsPodHealthy checks if a pod is healthy based on its status and optional health checks.
func (c *Checker) IsPodHealthy(ctx context.Context, pod *corev1.Pod, opts *HealthCheckOptions) (bool, error) {
	if pod.Status.Phase != corev1.PodRunning {
		return false, nil
	}

	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status != corev1.ConditionTrue {
			return false, nil
		}
	}

	if opts.HTTPCheckURL != "" {
		healthy, err := c.checkHTTPHealth(ctx, pod, opts.HTTPCheckURL, opts.HTTPCheckPort)
		if err != nil {
			return false, fmt.Errorf("http health check failed: %w", err)
		}
		if !healthy {
			return false, nil
		}
	}

	if opts.TCPPort > 0 {
		healthy, err := c.checkTCPPort(ctx, pod, opts.TCPPort)
		if err != nil {
			return false, fmt.Errorf("tcp port check failed: %w", err)
		}
		if !healthy {
			return false, nil
		}
	}

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

// buildHTTPHealthURL joins pod IP, port, and path into an HTTP URL.
// Port 0 (unset) uses defaultHTTPCheckPort. IPv6 addresses are bracketed.
func buildHTTPHealthURL(podIP string, port int, path string) string {
	if port <= 0 {
		port = defaultHTTPCheckPort
	}
	return "http://" + net.JoinHostPort(podIP, strconv.Itoa(port)) + path
}

// checkHTTPHealth performs an HTTP health check on a pod.
func (c *Checker) checkHTTPHealth(ctx context.Context, pod *corev1.Pod, healthCheckURL string, port int) (bool, error) {
	if pod.Status.PodIP == "" {
		return false, fmt.Errorf("pod IP is not available")
	}

	url := buildHTTPHealthURL(pod.Status.PodIP, port, healthCheckURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, nil // unreachable → unhealthy
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
}

// checkTCPPort checks if a TCP port is accepting connections.
func (c *Checker) checkTCPPort(ctx context.Context, pod *corev1.Pod, port int) (bool, error) {
	if pod.Status.PodIP == "" {
		return false, fmt.Errorf("pod IP is not available")
	}

	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(port)))
	if err != nil {
		return false, nil // not accepting connections → unhealthy
	}
	defer func() { _ = conn.Close() }()

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

	container := containerName
	if container == "" {
		container = pod.Spec.Containers[0].Name
	}

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

	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return false, nil // command failed or timed out → unhealthy
	}

	if expectedOutput != "" && !strings.Contains(stdout.String(), expectedOutput) {
		return false, nil
	}

	return true, nil
}
