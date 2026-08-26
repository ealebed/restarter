package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func TestChecker_IsPodHealthy(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		opts        HealthCheckOptions
		expected    bool
		expectError bool
	}{
		{
			name: "healthy pod without http check",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
					PodIP: "10.0.0.1",
				},
			},
			opts:        HealthCheckOptions{},
			expected:    true,
			expectError: false,
		},
		{
			name: "pod not running",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			opts:        HealthCheckOptions{},
			expected:    false,
			expectError: false,
		},
		{
			name: "pod not ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			opts:        HealthCheckOptions{},
			expected:    false,
			expectError: false,
		},
		{
			name: "running pod with no ready condition treated as healthy",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			opts:        HealthCheckOptions{},
			expected:    true,
			expectError: false,
		},
	}

	checker := NewChecker(5 * time.Second)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := checker.IsPodHealthy(ctx, tt.pod, &tt.opts)
			if (err != nil) != tt.expectError {
				t.Errorf("IsPodHealthy() error = %v, expectError %v", err, tt.expectError)
				return
			}
			if result != tt.expected {
				t.Errorf("IsPodHealthy() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNewChecker(t *testing.T) {
	timeout := 10 * time.Second
	checker := NewChecker(timeout)

	if checker == nil {
		t.Fatal("NewChecker() returned nil")
	}

	if checker.timeout != timeout {
		t.Errorf("NewChecker() timeout = %v, want %v", checker.timeout, timeout)
	}

	if checker.httpClient == nil {
		t.Fatal("NewChecker() httpClient is nil")
	}

	if checker.httpClient.Timeout != timeout {
		t.Errorf("NewChecker() httpClient.Timeout = %v, want %v", checker.httpClient.Timeout, timeout)
	}
}

func TestBuildHTTPHealthURL(t *testing.T) {
	tests := []struct {
		name  string
		podIP string
		port  int
		path  string
		want  string
	}{
		{name: "default port", podIP: "10.0.0.1", port: 0, path: "/health", want: "http://10.0.0.1:8080/health"},
		{name: "custom port", podIP: "10.0.0.1", port: 9090, path: "/status/health", want: "http://10.0.0.1:9090/status/health"},
		{name: "ipv6", podIP: "2001:db8::1", port: 8443, path: "/ready", want: "http://[2001:db8::1]:8443/ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildHTTPHealthURL(tt.podIP, tt.port, tt.path); got != tt.want {
				t.Errorf("buildHTTPHealthURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChecker_HTTPHealthUsesConfiguredPort(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
			PodIP: host,
		},
	}

	ok, err := NewChecker(2*time.Second).IsPodHealthy(context.Background(), pod, &HealthCheckOptions{
		HTTPCheckURL:  "/health",
		HTTPCheckPort: port,
	})
	if err != nil {
		t.Fatalf("IsPodHealthy() error = %v", err)
	}
	if !ok {
		t.Fatal("IsPodHealthy() = false, want true")
	}
}

func TestChecker_HTTPHealthMissingPodIP(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	_, err := NewChecker(time.Second).IsPodHealthy(context.Background(), pod, &HealthCheckOptions{
		HTTPCheckURL: "/health",
	})
	if err == nil {
		t.Fatal("IsPodHealthy() expected error when pod IP is missing")
	}
}
