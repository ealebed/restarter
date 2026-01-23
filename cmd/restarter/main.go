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

// getEnv returns the environment variable value or the default value if not set.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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
		mustParseDuration(getEnv("HEALTH_CHECK_TIMEOUT", "5s")),
		"Timeout for health checks (env: HEALTH_CHECK_TIMEOUT)")
	execCheckCommand = flag.String("exec-check-command", getEnv("EXEC_CHECK_COMMAND", ""),
		"Command to execute in container for health check (e.g., 'ps aux | grep java') (env: EXEC_CHECK_COMMAND)")
	execCheckContainer = flag.String("exec-check-container", getEnv("EXEC_CHECK_CONTAINER", ""),
		"Container name for exec check (empty for first container) (env: EXEC_CHECK_CONTAINER)")
	execCheckExpected = flag.String("exec-check-expected", getEnv("EXEC_CHECK_EXPECTED", ""),
		"Expected output from exec command (empty to just check exit code) (env: EXEC_CHECK_EXPECTED)")
	tcpCheckPort = flag.Int("tcp-check-port", mustParseInt(getEnv("TCP_CHECK_PORT", "0")),
		"TCP port to check for connectivity (0 to disable) (env: TCP_CHECK_PORT)")
)

// mustParseDuration parses a duration string or panics.
func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		// Return default if parsing fails
		return 5 * time.Second
	}
	return d
}

// mustParseInt parses an integer string or returns 0.
func mustParseInt(s string) int {
	if s == "" {
		return 0
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}

func main() {
	zopts := zap.Options{Development: false}
	zopts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Validate that at least one filtering method is provided
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

	// Create manager options
	opts := ctrl.Options{
		Scheme:                 scheme,
		LeaderElection:         false,                                    // Set to true for HA deployments
		Metrics:                server.Options{BindAddress: "0"},         // Disable metrics server
		HealthProbeBindAddress: getEnv("HEALTH_PROBE_BIND_ADDRESS", "0"), // Set to ":8080" to enable health probes
	}

	// Scope cache to the target namespace
	opts.Cache = cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			*namespace: {},
		},
	}

	// Create manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		log.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// Create health checker
	healthChecker := health.NewChecker(*healthCheckTimeout)

	// Set Kubernetes client for exec-based checks (if exec command is configured)
	if *execCheckCommand != "" {
		config := mgr.GetConfig()
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			log.Error(err, "Failed to create Kubernetes clientset for exec checks")
			os.Exit(1)
		}
		healthChecker.SetKubernetesClient(clientset, config)
	}

	// Build health check options
	healthCheckOptions := health.HealthCheckOptions{
		HTTPCheckURL:   *healthCheckURL,
		ExecCommand:    *execCheckCommand,
		TCPPort:        *tcpCheckPort,
		ContainerName:  *execCheckContainer,
		ExpectedOutput: *execCheckExpected,
	}

	// Setup controller
	if err := (&controller.PodReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		StatefulSetName:    *statefulSetName,
		PodLabelSelector:   *podLabelSelector,
		Namespace:          *namespace,
		HealthChecker:      healthChecker,
		HealthCheckOptions: healthCheckOptions,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Failed to setup controller")
		os.Exit(1)
	}

	// Start the manager
	log.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Manager exited with error")
		os.Exit(1)
	}
}
