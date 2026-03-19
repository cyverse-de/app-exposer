// Package main implements the vice-operator binary, a minimal K8s operator
// that receives pre-built resource bundles from app-exposer and applies them
// to the local cluster.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/operator"
	"github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

var log = common.Log

func main() {
	var (
		kubeconfig              string
		namespace               string
		port                    int
		gpuVendorFlag           string
		maxAnalyses             int
		nodeLabelSelector       string
		logLevel                string
		basicAuth               bool
		basicAuthUsername       string
		basicAuthPassword       string
		viceBaseURL             string
		clusterConfigSecret     string
		imagePullSecret         string
		registryServer          string
		registryUsername        string
		registryPassword        string
		loadingPort             int
		loadingServiceName      string
		loadingServicePort      int
		loadingTimeoutMs        int64
		loadingPodSelector      string
		gatewayName             string
		gatewayClassName        string
		gatewayEntryPort        int
		keycloakBaseURL         string
		keycloakRealm           string
		keycloakClientID        string
		keycloakClientSecret    string
		disableViceProxyAuth    bool
		enableLegacyAuth        bool
		checkResourceAccessBase string
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig (empty for in-cluster)")
	flag.StringVar(&namespace, "namespace", "vice-apps", "Namespace for VICE resources")
	flag.IntVar(&port, "port", 60001, "Listen port")
	flag.StringVar(&gpuVendorFlag, "gpu-vendor", "nvidia", "GPU vendor: nvidia or amd")
	flag.IntVar(&maxAnalyses, "max-analyses", 50, "Max concurrent analyses")
	flag.StringVar(&nodeLabelSelector, "node-label-selector", "", "Filter schedulable nodes by label")
	flag.StringVar(&logLevel, "log-level", "info", "Log level")
	flag.BoolVar(&basicAuth, "basic-auth", false, "Enable basic auth for the API")
	flag.StringVar(&basicAuthUsername, "basic-auth-username", "", "Basic auth username (required when --basic-auth is set)")
	flag.StringVar(&basicAuthPassword, "basic-auth-password", "", "Basic auth password (required when --basic-auth is set)")
	flag.StringVar(&viceBaseURL, "vice-base-url", "https://cyverse.run", "Base URL for VICE, stored in the cluster config secret")
	flag.StringVar(&clusterConfigSecret, "cluster-config-secret", "cluster-config-secret", "Name of the K8s Secret holding cluster config")
	flag.StringVar(&imagePullSecret, "image-pull-secret", "vice-image-pull-secret", "Name of the K8s image pull Secret")
	flag.StringVar(&registryServer, "registry-server", "", "Docker registry server (e.g. harbor.cyverse.org)")
	flag.StringVar(&registryUsername, "registry-username", "", "Docker registry username")
	flag.StringVar(&registryPassword, "registry-password", "", "Docker registry password")
	flag.IntVar(&loadingPort, "loading-port", 8080, "Listen port for loading page server")
	flag.StringVar(&loadingServiceName, "loading-service-name", "vice-operator-loading", "Name of the loading page service")
	flag.IntVar(&loadingServicePort, "loading-service-port", 80, "Port of the loading page service")
	flag.Int64Var(&loadingTimeoutMs, "loading-timeout-ms", 600000, "Loading page timeout in milliseconds")
	flag.StringVar(&loadingPodSelector, "loading-pod-selector", "", "Pod selector label for the loading page service (e.g. app=vice-operator-local); if set, ensures the service exists at startup")
	flag.StringVar(&gatewayName, "gateway-name", "vice", "Name of the Gateway resource")
	flag.StringVar(&gatewayClassName, "gateway-class-name", "traefik", "GatewayClass name for the Gateway resource")
	flag.IntVar(&gatewayEntryPort, "gateway-entrypoint-port", 8000, "Entrypoint port on the Gateway listener (must match the gateway controller's internal port)")
	flag.StringVar(&keycloakBaseURL, "keycloak-base-url", "", "Keycloak base URL for vice-proxy auth")
	flag.StringVar(&keycloakRealm, "keycloak-realm", "", "Keycloak realm for vice-proxy auth")
	flag.StringVar(&keycloakClientID, "keycloak-client-id", "", "OIDC client ID for vice-proxy auth")
	flag.StringVar(&keycloakClientSecret, "keycloak-client-secret", "", "OIDC client secret for vice-proxy auth")
	flag.BoolVar(&disableViceProxyAuth, "disable-vice-proxy-auth", false, "Disable auth in vice-proxy")
	flag.BoolVar(&enableLegacyAuth, "enable-legacy-auth", false, "Enable legacy auth in vice-proxy")
	flag.StringVar(&checkResourceAccessBase, "check-resource-access-base", "", "Legacy auth service URL for vice-proxy")
	flag.Parse()

	// Validate basic auth flags.
	if basicAuth && (basicAuthUsername == "" || basicAuthPassword == "") {
		log.Fatal("--basic-auth-username and --basic-auth-password are required when --basic-auth is enabled")
	}

	// Validate vice-base-url is a proper HTTP(S) URL.
	parsedURL, parseErr := url.Parse(viceBaseURL)
	if parseErr != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		log.Fatalf("--vice-base-url must be a valid HTTP(S) URL, got %q", viceBaseURL)
	}

	// Validate registry flags: all three required together, or none.
	registryFlagsSet := registryServer != "" || registryUsername != "" || registryPassword != ""
	if registryFlagsSet && (registryServer == "" || registryUsername == "" || registryPassword == "") {
		log.Fatal("--registry-server, --registry-username, and --registry-password must all be provided together")
	}

	// Configure log level.
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		log.Fatalf("invalid log level %q: %v", logLevel, err)
	}
	logrus.SetLevel(level)

	// Build K8s client.
	var config *rest.Config
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatalf("error building kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("error creating k8s client: %v", err)
	}

	// Create Gateway API client.
	gwClient, err := gatewayclient.NewForConfig(config)
	if err != nil {
		log.Fatalf("error creating gateway API client: %v", err)
	}

	// Build the cluster config map from flags. Only non-empty values are
	// included so that empty strings don't override vice-proxy defaults.
	clusterConfig := map[string]string{"VICE_BASE_URL": viceBaseURL}
	if keycloakBaseURL != "" {
		clusterConfig["KEYCLOAK_BASE_URL"] = keycloakBaseURL
	}
	if keycloakRealm != "" {
		clusterConfig["KEYCLOAK_REALM"] = keycloakRealm
	}
	if keycloakClientID != "" {
		clusterConfig["KEYCLOAK_CLIENT_ID"] = keycloakClientID
	}
	if keycloakClientSecret != "" {
		clusterConfig["KEYCLOAK_CLIENT_SECRET"] = keycloakClientSecret
	}
	if disableViceProxyAuth {
		clusterConfig["DISABLE_AUTH"] = "true"
	}
	if enableLegacyAuth {
		clusterConfig["ENABLE_LEGACY_AUTH"] = "true"
	}
	if checkResourceAccessBase != "" {
		clusterConfig["CHECK_RESOURCE_ACCESS_BASE"] = checkResourceAccessBase
	}

	// Ensure the cluster config secret exists with the correct values
	// before starting the operator, so vice-proxy containers can reference it
	// as env vars via EnvFrom.
	configCtx, configCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer configCancel()
	if err := operator.EnsureClusterConfigSecret(configCtx, clientset, namespace, clusterConfigSecret, clusterConfig); err != nil {
		log.Fatalf("failed to ensure cluster config secret: %v", err)
	}

	// Ensure the image pull secret exists so pods can pull from private registries.
	if registryServer != "" {
		pullCtx, pullCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer pullCancel()
		if err := operator.EnsureImagePullSecret(pullCtx, clientset, namespace, imagePullSecret, registryServer, registryUsername, registryPassword); err != nil {
			log.Fatalf("failed to ensure image pull secret: %v", err)
		}
	}

	// Ensure the loading page service exists so HTTPRoutes have a valid backend.
	if loadingPodSelector != "" {
		selector, selectorErr := parseSelector(loadingPodSelector)
		if selectorErr != nil {
			log.Fatalf("invalid --loading-pod-selector: %v", selectorErr)
		}
		loadingSvcCtx, loadingSvcCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer loadingSvcCancel()
		if err := operator.EnsureLoadingService(loadingSvcCtx, clientset, namespace, loadingServiceName, int32(loadingPort), selector); err != nil {
			log.Fatalf("failed to ensure loading service: %v", err)
		}
	}

	// Ensure the Gateway resource exists so HTTPRoutes can attach to it.
	gwCtx, gwCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer gwCancel()
	if err := operator.EnsureGateway(gwCtx, gwClient, namespace, gatewayName, gatewayClassName, int32(gatewayEntryPort)); err != nil {
		log.Fatalf("failed to ensure gateway: %v", err)
	}

	// Ensure the CORS middleware exists so HTTPRoutes can reference it.
	corsCtx, corsCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer corsCancel()
	if err := operator.EnsureCORSMiddleware(corsCtx, clientset, namespace); err != nil {
		log.Fatalf("failed to ensure CORS middleware: %v", err)
	}

	gpuVendor, err := operator.ParseGPUVendor(gpuVendorFlag)
	if err != nil {
		log.Fatalf("invalid GPU vendor: %v", err)
	}

	// Extract the base domain from vice-base-url for hostname rewriting
	// (e.g. "https://localhost" → "localhost").
	baseDomain := parsedURL.Host

	capacityCalc := operator.NewCapacityCalculator(clientset, namespace, maxAnalyses, nodeLabelSelector)
	imageCache := operator.NewImageCacheManager(clientset, namespace, imagePullSecret)
	op := operator.NewOperator(clientset, gwClient, namespace, gpuVendor, capacityCalc, imageCache,
		loadingServiceName, int32(loadingServicePort), loadingTimeoutMs, baseDomain)

	app := NewApp(op, basicAuth, basicAuthUsername, basicAuthPassword)
	loadingApp := NewLoadingApp(op)

	apiAddr := fmt.Sprintf(":%d", port)
	loadingAddr := fmt.Sprintf(":%d", loadingPort)

	log.Infof("vice-operator listening on %s (loading page on %s, namespace=%s, gpu-vendor=%s, vice-base-url=%s, max-analyses=%d)",
		apiAddr, loadingAddr, namespace, gpuVendorFlag, viceBaseURL, maxAnalyses)

	// Start loading page server in a goroutine.
	go func() {
		log.Infof("loading page server starting on %s", loadingAddr)
		if err := loadingApp.Start(loadingAddr); err != nil {
			log.Fatalf("loading page server failed: %v", err)
		}
	}()

	// API server blocks on the main goroutine.
	if err := app.Start(apiAddr); err != nil {
		log.Error(err)
		os.Exit(1)
	}
}

// parseSelector parses a comma-separated "key=value,key2=value2" string into
// a label map suitable for a Service selector.
func parseSelector(s string) (map[string]string, error) {
	result := make(map[string]string)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			return nil, fmt.Errorf("invalid selector term %q (expected key=value)", part)
		}
		result[kv[0]] = kv[1]
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("selector must contain at least one key=value pair")
	}
	return result, nil
}
