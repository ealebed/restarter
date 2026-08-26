package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ealebed/restarter/internal/health"
)

const testNS = "druid"

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func runningReadyPod(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "pause"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
			PodIP: "10.0.0.1",
		},
	}
}

func notReadyPod(name string, labels map[string]string) *corev1.Pod {
	pod := runningReadyPod(name, labels)
	pod.Status.Conditions = []corev1.PodCondition{{
		Type:   corev1.PodReady,
		Status: corev1.ConditionFalse,
	}}
	return pod
}

func webStatefulSet() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: testNS},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "web"},
			},
		},
	}
}

func newReconciler(t *testing.T, objects ...client.Object) (*PodReconciler, client.Client) {
	t.Helper()
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &PodReconciler{
		Client:          c,
		Scheme:          scheme,
		Namespace:       testNS,
		HealthChecker:   health.NewChecker(time.Second),
		StatefulSetName: "web",
	}, c
}

func TestReconcile_PodNotFound(t *testing.T) {
	r, _ := newReconciler(t)
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNS, Name: "missing"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("Reconcile() result = %+v, want empty", res)
	}
}

func TestReconcile_SkipsPodOutsideFilter(t *testing.T) {
	other := runningReadyPod("other-0", map[string]string{"app": "other"})
	r, c := newReconciler(t, webStatefulSet(), other)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNS, Name: other.Name},
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var got corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(other), &got); err != nil {
		t.Fatalf("pod should still exist: %v", err)
	}
}

func TestReconcile_LeavesHealthyPod(t *testing.T) {
	pod := runningReadyPod("web-0", map[string]string{"app": "web"})
	r, c := newReconciler(t, webStatefulSet(), pod)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNS, Name: pod.Name},
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var got corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(pod), &got); err != nil {
		t.Fatalf("healthy pod should not be deleted: %v", err)
	}
}

func TestReconcile_DeletesUnhealthyPod(t *testing.T) {
	pod := notReadyPod("web-0", map[string]string{"app": "web"})
	r, c := newReconciler(t, webStatefulSet(), pod)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNS, Name: pod.Name},
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var got corev1.Pod
	err := c.Get(context.Background(), client.ObjectKeyFromObject(pod), &got)
	if err == nil {
		t.Fatal("unhealthy pod should have been deleted")
	}
	if client.IgnoreNotFound(err) != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
}

func TestReconcile_RequeuesOnHealthCheckError(t *testing.T) {
	pod := runningReadyPod("web-0", map[string]string{"app": "web"})
	pod.Status.PodIP = ""
	r, c := newReconciler(t, webStatefulSet(), pod)
	r.HealthCheckOptions = health.HealthCheckOptions{HTTPCheckURL: "/health"}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNS, Name: pod.Name},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if res.RequeueAfter != requeueDelay {
		t.Fatalf("Reconcile() RequeueAfter = %v, want %v", res.RequeueAfter, requeueDelay)
	}

	var got corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(pod), &got); err != nil {
		t.Fatalf("pod should not be deleted on health-check error: %v", err)
	}
}

func TestMatchesFilter(t *testing.T) {
	stsPod := runningReadyPod("web-0", map[string]string{"app": "web"})
	otherPod := runningReadyPod("other-0", map[string]string{"app": "other"})
	labeled := runningReadyPod("router-0", map[string]string{"app": "router", "component": "druid"})

	tests := []struct {
		name             string
		statefulSetName  string
		podLabelSelector string
		objects          []client.Object
		pod              *corev1.Pod
		want             bool
	}{
		{
			name:            "statefulset match",
			statefulSetName: "web",
			objects:         []client.Object{webStatefulSet()},
			pod:             stsPod,
			want:            true,
		},
		{
			name:            "statefulset mismatch",
			statefulSetName: "web",
			objects:         []client.Object{webStatefulSet()},
			pod:             otherPod,
			want:            false,
		},
		{
			name:            "missing statefulset",
			statefulSetName: "web",
			pod:             stsPod,
			want:            false,
		},
		{
			name:             "label selector match",
			podLabelSelector: "app=router,component=druid",
			pod:              labeled,
			want:             true,
		},
		{
			name:             "label selector mismatch",
			podLabelSelector: "app=router,component=druid",
			pod:              stsPod,
			want:             false,
		},
		{
			name:             "invalid selector",
			podLabelSelector: "!!!",
			pod:              labeled,
			want:             false,
		},
		{
			name:             "statefulset and labels both match",
			statefulSetName:  "web",
			podLabelSelector: "app=web",
			objects:          []client.Object{webStatefulSet()},
			pod:              stsPod,
			want:             true,
		},
		{
			name:             "statefulset match but labels do not",
			statefulSetName:  "web",
			podLabelSelector: "app=router",
			objects:          []client.Object{webStatefulSet()},
			pod:              stsPod,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := newReconciler(t, tt.objects...)
			r.StatefulSetName = tt.statefulSetName
			r.PodLabelSelector = tt.podLabelSelector
			if got := r.matchesFilter(context.Background(), tt.pod); got != tt.want {
				t.Fatalf("matchesFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}
