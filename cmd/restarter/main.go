package main

import (
	"flag"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/ealebed/restarter/internal/controller"
	"github.com/ealebed/restarter/internal/health"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseDurationOrDefault(s string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func parseIntOrDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return i
}

var (
	namespace = flag.String("namespace", getEnv("NAMESPACE", "default"),
		"Kubernetes namespace (env: NAMESPACE)")
	statefulSetName = flag.String("statefulset", getEnv("STATEFULSET_NAME", ""),
		"StatefulSet name to monitor (env: STATEFULSET_NAME)")
	podLabelSelector = flag.String("pod-label-selector", getEnv("POD_LABEL_SELECTOR", ""),
		"Pod label selector (e.g., 'app=router,component=druid') (env: POD_LABEL_SELECTOR)")
	healthCheckURL = flag.String("health-check-url", getEnv("HEALTH_CHECK_URL", ""),
		"HTTP health check URL path (e.g., /health) (env: HEALTH_CHECK_URL)")
	healthCheckTimeout = flag.Duration("health-check-timeout",
		parseDurationOrDefault(getEnv("HEALTH_CHECK_TIMEOUT", "5s"), 5*time.Second),
		"Timeout for health checks (env: HEALTH_CHECK_TIMEOUT)")
	execCheckCommand = flag.String("exec-check-command", getEnv("EXEC_CHECK_COMMAND", ""),
		"Command to execute in container for health check (e.g., 'ps aux | grep java') (env: EXEC_CHECK_COMMAND)")
	execCheckContainer = flag.String("exec-check-container", getEnv("EXEC_CHECK_CONTAINER", ""),
		"Container name for exec check (empty for first container) (env: EXEC_CHECK_CONTAINER)")
	execCheckExpected = flag.String("exec-check-expected", getEnv("EXEC_CHECK_EXPECTED", ""),
		"Expected output from exec command (empty to just check exit code) (env: EXEC_CHECK_EXPECTED)")
	tcpCheckPort = flag.Int("tcp-check-port", parseIntOrDefault(getEnv("TCP_CHECK_PORT", "0"), 0),
		"TCP port to check for connectivity (0 to disable) (env: TCP_CHECK_PORT)")
)

func main() {
	zopts := zap.Options{Development: false}
	zopts.BindFlags(flag.CommandLine)
	flag.Parse()

	if *statefulSetName == "" && *podLabelSelector == "" {
		ctrl.Log.Error(nil, "Either --statefulset or --pod-label-selector (or both) must be provided")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	log := ctrl.Log.WithName("restarter")

	log.Info("Starting restarter controller",
		"namespace", *namespace,
		"statefulset", *statefulSetName,
		"podLabelSelector", *podLabelSelector,
		"healthCheckURL", *healthCheckURL,
		"healthCheckTimeout", *healthCheckTimeout,
		"execCheckCommand", *execCheckCommand,
		"tcpCheckPort", *tcpCheckPort,
	)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		LeaderElection:         false,
		Metrics:                server.Options{BindAddress: "0"},
		HealthProbeBindAddress: getEnv("HEALTH_PROBE_BIND_ADDRESS", "0"),
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				*namespace: {},
			},
		},
	})
	if err != nil {
		log.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	healthChecker := health.NewChecker(*healthCheckTimeout)
	if *execCheckCommand != "" {
		clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
		if err != nil {
			log.Error(err, "Failed to create Kubernetes clientset for exec checks")
			os.Exit(1)
		}
		healthChecker.SetKubernetesClient(clientset, mgr.GetConfig())
	}

	if err := (&controller.PodReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		StatefulSetName:  *statefulSetName,
		PodLabelSelector: *podLabelSelector,
		Namespace:        *namespace,
		HealthChecker:    healthChecker,
		HealthCheckOptions: health.HealthCheckOptions{
			HTTPCheckURL:   *healthCheckURL,
			ExecCommand:    *execCheckCommand,
			TCPPort:        *tcpCheckPort,
			ContainerName:  *execCheckContainer,
			ExpectedOutput: *execCheckExpected,
		},
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Failed to setup controller")
		os.Exit(1)
	}

	log.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Manager exited with error")
		os.Exit(1)
	}
}
