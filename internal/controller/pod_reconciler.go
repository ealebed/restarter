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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/ealebed/restarter/internal/health"
)

const requeueDelay = 10 * time.Second

// PodReconciler reconciles Pods based on StatefulSet name and/or label selector.
type PodReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	StatefulSetName    string
	PodLabelSelector   string
	Namespace          string
	HealthChecker      *health.Checker
	HealthCheckOptions health.HealthCheckOptions

	parsedLabelSelector labels.Selector
}

// Reconcile is called whenever a Pod is created, updated, or deleted.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("pod", req.NamespacedName)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get pod: %w", err)
	}

	if !r.matchesFilter(ctx, &pod) {
		logger.V(1).Info("Pod does not match filter criteria, skipping")
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling pod", "phase", pod.Status.Phase)

	healthy, err := r.HealthChecker.IsPodHealthy(ctx, &pod, &r.HealthCheckOptions)
	if err != nil {
		logger.Error(err, "Failed to check pod health")
		return ctrl.Result{RequeueAfter: requeueDelay}, nil
	}

	if healthy {
		logger.V(1).Info("Pod is healthy")
		return ctrl.Result{}, nil
	}

	logger.Info("Pod is unhealthy, triggering restart")
	if err := r.Delete(ctx, &pod); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to delete pod")
		return ctrl.Result{RequeueAfter: requeueDelay}, nil
	}

	logger.Info("Successfully triggered pod restart")
	return ctrl.Result{}, nil
}

// matchesFilter checks if a pod matches the filtering criteria (StatefulSet and/or label selector).
func (r *PodReconciler) matchesFilter(ctx context.Context, pod *corev1.Pod) bool {
	if r.StatefulSetName != "" && !r.belongsToStatefulSet(ctx, pod) {
		return false
	}

	if r.PodLabelSelector == "" {
		return true
	}

	selector := r.parsedLabelSelector
	if selector == nil {
		var err error
		selector, err = labels.Parse(r.PodLabelSelector)
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to parse pod label selector")
			return false
		}
	}

	return selector.Matches(labels.Set(pod.Labels))
}

// belongsToStatefulSet checks if a pod belongs to the target StatefulSet.
func (r *PodReconciler) belongsToStatefulSet(ctx context.Context, pod *corev1.Pod) bool {
	var statefulSet appsv1.StatefulSet
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: r.Namespace,
		Name:      r.StatefulSetName,
	}, &statefulSet); err != nil {
		log.FromContext(ctx).Error(err, "Failed to get StatefulSet")
		return false
	}

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
	predicates := []predicate.Predicate{
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == r.Namespace
		}),
	}

	if r.PodLabelSelector != "" {
		selector, err := labels.Parse(r.PodLabelSelector)
		if err != nil {
			return fmt.Errorf("failed to parse pod label selector: %w", err)
		}
		r.parsedLabelSelector = selector
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
