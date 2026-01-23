package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/ealebed/restarter/internal/health"
	ctrl "sigs.k8s.io/controller-runtime"
)

// PodReconciler reconciles Pods based on StatefulSet name and/or label selector.
type PodReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	StatefulSetName    string
	PodLabelSelector   string
	Namespace          string
	HealthChecker      *health.Checker
	HealthCheckOptions health.HealthCheckOptions
}

// Reconcile is called whenever a Pod is created, updated, or deleted.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("pod", req.NamespacedName)

	// Get the pod
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if errors.IsNotFound(err) {
			// Pod was deleted, nothing to do
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get pod: %w", err)
	}

	// Verify the pod matches our filtering criteria
	if !r.matchesFilter(ctx, &pod) {
		logger.V(1).Info("Pod does not match filter criteria, skipping")
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling pod", "phase", pod.Status.Phase)

	// Check if pod is healthy
	healthy, err := r.HealthChecker.IsPodHealthy(ctx, &pod, r.HealthCheckOptions)
	if err != nil {
		logger.Error(err, "Failed to check pod health")
		// Requeue after a short delay to retry
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if !healthy {
		logger.Info("Pod is unhealthy, triggering restart")

		// Also validate pod status
		if !r.HealthChecker.CheckPodStatus(&pod) {
			logger.Info("Pod status check failed, proceeding with restart")
		}

		// Delete the pod to trigger restart by StatefulSet controller
		if err := r.Delete(ctx, &pod); err != nil {
			if errors.IsNotFound(err) {
				// Pod was already deleted, nothing to do
				return ctrl.Result{}, nil
			}
			logger.Error(err, "Failed to delete pod")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		logger.Info("Successfully triggered pod restart")
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("Pod is healthy")
	return ctrl.Result{}, nil
}

// matchesFilter checks if a pod matches the filtering criteria (StatefulSet and/or label selector).
func (r *PodReconciler) matchesFilter(ctx context.Context, pod *corev1.Pod) bool {
	// If StatefulSet name is provided, verify pod belongs to it
	if r.StatefulSetName != "" {
		if !r.belongsToStatefulSet(ctx, pod) {
			return false
		}
	}

	// If pod label selector is provided, verify pod matches it
	if r.PodLabelSelector != "" {
		selector, err := labels.Parse(r.PodLabelSelector)
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to parse pod label selector")
			return false
		}
		if !selector.Matches(labels.Set(pod.Labels)) {
			return false
		}
	}

	return true
}

// belongsToStatefulSet checks if a pod belongs to the target StatefulSet.
func (r *PodReconciler) belongsToStatefulSet(ctx context.Context, pod *corev1.Pod) bool {
	// Get the StatefulSet
	var statefulSet appsv1.StatefulSet
	statefulSetKey := types.NamespacedName{
		Namespace: r.Namespace,
		Name:      r.StatefulSetName,
	}

	if err := r.Get(ctx, statefulSetKey, &statefulSet); err != nil {
		log.FromContext(ctx).Error(err, "Failed to get StatefulSet")
		return false
	}

	// Check if pod has the StatefulSet's selector labels
	if statefulSet.Spec.Selector == nil {
		return false
	}

	selector, err := metav1.LabelSelectorAsSelector(statefulSet.Spec.Selector)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to create selector")
		return false
	}

	return selector.Matches(labels.Set(pod.Labels))
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create predicates to filter pods
	predicates := []predicate.Predicate{
		// Filter by namespace
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == r.Namespace
		}),
	}

	// If pod label selector is provided, add it as a predicate for efficiency
	if r.PodLabelSelector != "" {
		selector, err := labels.Parse(r.PodLabelSelector)
		if err != nil {
			return fmt.Errorf("failed to parse pod label selector: %w", err)
		}
		predicates = append(predicates, predicate.NewPredicateFuncs(func(obj client.Object) bool {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return false
			}
			return selector.Matches(labels.Set(pod.Labels))
		}))
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates(predicates...)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
