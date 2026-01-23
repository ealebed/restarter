package health

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func TestChecker_CheckPodStatus(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "healthy pod",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
						{
							Type:   corev1.PodScheduled,
							Status: corev1.ConditionTrue,
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: true,
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{},
							},
						},
					},
				},
			},
			expected: true,
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
			expected: false,
		},
		{
			name: "container not ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: false,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "container waiting",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: true,
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{},
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	checker := NewChecker(5 * time.Second)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.CheckPodStatus(tt.pod)
			if result != tt.expected {
				t.Errorf("CheckPodStatus() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestChecker_IsPodHealthy(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		opts     HealthCheckOptions
		expected bool
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
			opts:     HealthCheckOptions{},
			expected: true,
			expectError: false,
		},
		{
			name: "pod not running",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			opts:     HealthCheckOptions{},
			expected: false,
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
			opts:     HealthCheckOptions{},
			expected: false,
			expectError: false,
		},
	}

	checker := NewChecker(5 * time.Second)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := checker.IsPodHealthy(ctx, tt.pod, tt.opts)
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
