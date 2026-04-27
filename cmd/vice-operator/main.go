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

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operator"
	"github.com/sirupsen/logrus"
)

var log = common.Log

func main() {
	var (
		kubeconfig            string
		namespace             string
		port                  int
		gpuVendorFlag         string
		maxAnalyses           int
		nodeLabelSelector     string
		logLevel              string
		apiAuth               bool
		apiAuthIssuerURL      string
		apiAuthClientID       string
		viceBaseURL           string
		clusterConfigSecret   string
		imagePullSecret       string
		registryServer        string
		registryUsername      string
		registryPassword      string
		loadingPort           int
		loadingServiceName    string
		loadingServicePort    int
		loadingTimeoutMs      int64
		operatorPodSelector   string
		gatewayNamespace      string
		gatewayName           string
		gatewayClassName      string
		gatewayEntryPort      int
		gatewaySkipCreation   bool
		keycloakBaseURL       string
		keycloakRealm         string
		keycloakClientID      string
		keycloakClientSecret  string
		disableViceProxyAuth  bool
		swaggerClientID       string
		swaggerClientSecret   string
		swaggerCookieSecret   string
		apiSubdomain          string
		apiServiceName        string
		serviceCIDR           string
		blockedCIDRs          stringSliceFlag
		egressPodExceptions   stringSliceFlag
		egressHostExceptions  stringSliceFlag
		egressCIDRExceptions  stringSliceFlag
		ingressPodExceptions  stringSliceFlag
		disableInternetAccess bool
		userSuffix            string
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig (empty for in-cluster)")
	flag.StringVar(&namespace, "namespace", "vice-apps", "Namespace for VICE resources")
	flag.IntVar(&port, "port", 60001, "Listen port")
	flag.StringVar(&gpuVendorFlag, "gpu-vendor", "nvidia", "GPU vendor: nvidia or amd")
	flag.IntVar(&maxAnalyses, "max-analyses", 50, "Max concurrent analyses (0 disables the limit for autoscaling clusters)")
	flag.StringVar(&nodeLabelSelector, "node-label-selector", "", "Filter schedulable nodes by label")
	flag.StringVar(&logLevel, "log-level", "info", "Log level")
	flag.BoolVar(&apiAuth, "api-auth", true, "Enable OIDC JWT Bearer auth for the API")
	flag.StringVar(&apiAuthIssuerURL, "api-auth-issuer-url", "", "OIDC issuer URL for API auth (e.g. https://keycloak.example.com/realms/cyverse)")
	flag.StringVar(&apiAuthClientID, "api-auth-client-id", "", "Expected client ID (azp claim) for API auth")
	flag.StringVar(&viceBaseURL, "vice-base-url", "https://cyverse.run", "Base URL for VICE, stored in the cluster config secret")
	flag.StringVar(&clusterConfigSecret, "cluster-config-secret", "cluster-config-secret", "Name of the K8s Secret holding cluster config")
	flag.StringVar(&imagePullSecret, "image-pull-secret", "vice-image-pull-secret", "Name of the K8s image pull Secret")
	flag.StringVar(&registryServer, "registry-server", "", "Docker registry server (e.g. harbor.cyverse.org)")
	flag.StringVar(&registryUsername, "registry-username", "", "Docker registry username")
	flag.StringVar(&registryPassword, "registry-password", "", "Docker registry password (or REGISTRY_PASSWORD env var)")
	flag.IntVar(&loadingPort, "loading-port", 8080, "Listen port for loading page server")
	flag.StringVar(&loadingServiceName, "loading-service-name", "vice-operator-loading", "Name of the loading page service")
	flag.IntVar(&loadingServicePort, "loading-service-port", 80, "Port of the loading page service")
	flag.Int64Var(&loadingTimeoutMs, "loading-timeout-ms", 600000, "Loading page timeout in milliseconds")
	flag.StringVar(&operatorPodSelector, "operator-pod-selector", "", "Pod selector for vice-operator services (e.g. app=vice-operator-local); if set, ensures API and loading services exist at startup")
	flag.StringVar(&gatewayNamespace, "gateway-namespace", "", "Namespace of the Gateway resource (defaults to --namespace)")
	flag.StringVar(&gatewayName, "gateway-name", "vice", "Name of the Gateway resource")
	flag.StringVar(&gatewayClassName, "gateway-class-name", "traefik", "GatewayClass name for the Gateway resource")
	flag.IntVar(&gatewayEntryPort, "gateway-entrypoint-port", 8000, "Entrypoint port on the Gateway listener (must match the gateway controller's internal port)")
	flag.BoolVar(&gatewaySkipCreation, "gateway-skip-creation", false, "Skip creation of the Gateway resource (use when attaching to a pre-existing Gateway)")
	flag.StringVar(&keycloakBaseURL, "keycloak-base-url", "", "Keycloak base URL for vice-proxy auth")
	flag.StringVar(&keycloakRealm, "keycloak-realm", "", "Keycloak realm for vice-proxy auth")
	flag.StringVar(&keycloakClientID, "keycloak-client-id", "", "OIDC client ID for vice-proxy auth")
	flag.StringVar(&keycloakClientSecret, "keycloak-client-secret", "", "OIDC client secret for vice-proxy auth (or KEYCLOAK_CLIENT_SECRET env var)")
	flag.BoolVar(&disableViceProxyAuth, "disable-vice-proxy-auth", false, "Disable auth in vice-proxy")
	flag.StringVar(&swaggerClientID, "swagger-client-id", "", "OAuth2 client ID for the Swagger UI login flow (must support authorization code flow in Keycloak)")
	flag.StringVar(&swaggerClientSecret, "swagger-client-secret", "", "OAuth2 client secret for the Swagger UI login flow (or SWAGGER_CLIENT_SECRET env var)")
	flag.StringVar(&swaggerCookieSecret, "swagger-cookie-secret", "", "Secret for signing session cookies (random string; auto-generated if empty; or SWAGGER_COOKIE_SECRET env var)")
	flag.StringVar(&apiSubdomain, "api-subdomain", "vice-api", "Subdomain prefix for the vice-operator API HTTPRoute; combined with --vice-base-url host to form the full hostname")
	flag.StringVar(&apiServiceName, "api-service-name", "vice-operator", "K8s Service name for the vice-operator API HTTPRoute backend")
	flag.StringVar(&serviceCIDR, "service-cidr", "", "Cluster service CIDR to block in egress (auto-detected from kubernetes API server if empty)")
	flag.Var(&blockedCIDRs, "blocked-cidr", "Additional CIDRs to block in egress (repeatable)")
	flag.Var(&egressPodExceptions, "egress-pod-exception", "Pod selector label (key=value) to allow egress to (repeatable)")
	flag.Var(&egressHostExceptions, "egress-host-exception", "Hostname or IP that analyses should be able to reach; resolved to IPs at startup (repeatable)")
	flag.Var(&egressCIDRExceptions, "egress-cidr-exception", "CIDR (e.g. 10.0.0.0/8) that analyses should be able to reach (repeatable)")
	flag.Var(&ingressPodExceptions, "ingress-pod-exception", "Cross-namespace ingress source as kubernetes.io/metadata.name=<ns>,pod-label=val (repeatable). The kubernetes.io/metadata.name pair selects the namespace; remaining pairs select pods.")
	flag.BoolVar(&disableInternetAccess, "disable-internet-access", false, "Block analysis pods from reaching the public internet; only DNS, explicit host/CIDR exceptions, and pod exceptions are allowed")
	flag.StringVar(&userSuffix, "user-suffix", constants.DefaultUserSuffix, "Domain suffix appended to usernames if not already present")
	flag.Parse()

	// Allow secrets to come from environment variables when not set on the
	// command line. Avoids exposing them in process listings.
	envFallback := func(val *string, envKey string) {
		if *val == "" {
			*val = os.Getenv(envKey)
		}
	}
	envFallback(&registryPassword, "REGISTRY_PASSWORD")
	envFallback(&keycloakClientSecret, "KEYCLOAK_CLIENT_SECRET")
	envFallback(&swaggerClientSecret, "SWAGGER_CLIENT_SECRET")
	envFallback(&swaggerCookieSecret, "SWAGGER_COOKIE_SECRET")

	// Validate OIDC auth flags.
	if apiAuth && (apiAuthIssuerURL == "" || apiAuthClientID == "") {
		log.Fatal("--api-auth-issuer-url and --api-auth-client-id are required when --api-auth is enabled")
	}

	// Validate vice-base-url is a proper HTTP(S) URL.
	parsedURL, parseErr := url.Parse(viceBaseURL)
	if parseErr != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		log.Fatalf("--vice-base-url must be a valid HTTP(S) URL, got %q", viceBaseURL)
	}

	// Extract the base domain from vice-base-url for hostname rewriting and
	// wildcard route creation (e.g. "https://my-own-domain.org" → "my-own-domain.org").
	// Use Hostname() to strip any port, since HTTPRoute hostnames should not include ports.
	baseDomain := parsedURL.Hostname()

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

	clientset, gwClient, err := buildKubeClients(kubeconfig)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Build the cluster config map from flags. All keys are always written so
	// that stale values from a previous run are overwritten. The secret update
	// replaces the entire data map — omitting a key here removes it from the
	// secret.
	clusterConfig := map[string]string{
		"VICE_BASE_URL":          viceBaseURL,
		"KEYCLOAK_BASE_URL":      keycloakBaseURL,
		"KEYCLOAK_REALM":         keycloakRealm,
		"KEYCLOAK_CLIENT_ID":     keycloakClientID,
		"KEYCLOAK_CLIENT_SECRET": keycloakClientSecret,
	}
	if disableViceProxyAuth {
		clusterConfig["DISABLE_AUTH"] = "true"
	} else {
		clusterConfig["DISABLE_AUTH"] = "false"

		// Warn early if auth is enabled but Keycloak settings are missing,
		// since vice-proxy pods will crash-loop with a fatal validation error.
		if keycloakBaseURL == "" || keycloakRealm == "" || keycloakClientID == "" || keycloakClientSecret == "" {
			log.Warn("auth is enabled (--disable-vice-proxy-auth not set) but one or more Keycloak flags are empty; vice-proxy pods will fail to start")
		}
	}

	// Ensure cluster config secret so vice-proxy containers can reference it
	// as env vars via EnvFrom.
	mustEnsure("cluster config secret", func(ctx context.Context) error {
		return operator.EnsureClusterConfigSecret(ctx, clientset, namespace, clusterConfigSecret, clusterConfig)
	})

	// Ensure the image pull secret so pods can pull from private registries.
	if registryServer != "" {
		mustEnsure("image pull secret", func(ctx context.Context) error {
			return operator.EnsureImagePullSecret(ctx, clientset, namespace, imagePullSecret, registryServer, registryUsername, registryPassword)
		})
	}

	// Ensure the operator's K8s Services and API HTTPRoute exist so traffic
	// can reach the operator through the Gateway.
	if operatorPodSelector != "" {
		selector, selectorErr := parseSelector(operatorPodSelector)
		if selectorErr != nil {
			log.Fatalf("invalid --operator-pod-selector: %v", selectorErr)
		}

		// Loading page service: exposes loadingServicePort, routes to the
		// container's loadingPort (the port the loading server actually binds).
		mustEnsure("loading service", func(ctx context.Context) error {
			return operator.EnsureService(ctx, clientset, namespace, loadingServiceName, int32(loadingServicePort), int32(loadingPort), selector)
		})

		// API service (port → port, same as the operator's listen port).
		mustEnsure("API service", func(ctx context.Context) error {
			return operator.EnsureService(ctx, clientset, namespace, apiServiceName, int32(port), int32(port), selector)
		})

		// API HTTPRoute: makes the vice-operator API accessible through
		// the Gateway (e.g. for HAProxy / tailscale serve).
		apiHostname := fmt.Sprintf("%s.%s", apiSubdomain, parsedURL.Hostname())
		mustEnsure("API HTTPRoute", func(ctx context.Context) error {
			gwNS := gatewayNamespace
			if gwNS == "" {
				gwNS = namespace
			}
			return operator.EnsureAPIRoute(ctx, gwClient, namespace, gwNS, gatewayName, apiHostname, apiServiceName, int32(port))
		})
	}

	// Ensure the Gateway resource exists so HTTPRoutes can attach to it.
	// We skip this if --gateway-skip-creation is set, or if --gateway-namespace
	// points to a different namespace than the operator.
	shouldEnsureGateway := !gatewaySkipCreation && (gatewayNamespace == "" || gatewayNamespace == namespace)
	if shouldEnsureGateway {
		mustEnsure("gateway", func(ctx context.Context) error {
			return operator.EnsureGateway(ctx, gwClient, namespace, gatewayName, gatewayClassName, int32(gatewayEntryPort))
		})
	} else {
		log.Infof("skipping Gateway creation (skip-flag=%v, namespace=%s, gateway-namespace=%s)",
			gatewaySkipCreation, namespace, gatewayNamespace)
	}

	// Ensure the wildcard default HTTPRoute so subdomain requests arriving
	// before the analysis-specific HTTPRoute is created get a waiting page
	// instead of nothing. Only applies when the operator manages its own
	// Gateway (external clusters); in the local cluster where
	// --gateway-skip-creation is set, vice-default-backend handles this.
	if shouldEnsureGateway && operatorPodSelector != "" {
		mustEnsure("default wildcard HTTPRoute", func(ctx context.Context) error {
			gwNS := gatewayNamespace
			if gwNS == "" {
				gwNS = namespace
			}
			return operator.EnsureDefaultRoute(ctx, gwClient, namespace, gwNS, gatewayName, baseDomain, loadingServiceName, int32(loadingServicePort))
		})
	}

	// Ensure the CORS middleware exists so HTTPRoutes can reference it.
	mustEnsure("CORS middleware", func(ctx context.Context) error {
		return operator.EnsureCORSMiddleware(ctx, clientset, namespace)
	})

	// Build the egress / network-policy configuration from flags. Auto-
	// detects service CIDR, parses pod and ingress exceptions, resolves
	// Keycloak / host-exception CIDRs.
	egressConfig, err := buildEgressConfig(context.Background(), clientset, egressInputs{
		namespace:             namespace,
		serviceCIDR:           serviceCIDR,
		blockedCIDRs:          blockedCIDRs,
		egressPodExceptions:   egressPodExceptions,
		egressHostExceptions:  egressHostExceptions,
		egressCIDRExceptions:  egressCIDRExceptions,
		ingressPodExceptions:  ingressPodExceptions,
		disableInternetAccess: disableInternetAccess,
		operatorPodSelector:   operatorPodSelector,
		keycloakBaseURL:       keycloakBaseURL,
		disableViceProxyAuth:  disableViceProxyAuth,
	})
	if err != nil {
		log.Fatalf("%v", err)
	}

	mustEnsure("network policies", func(ctx context.Context) error {
		return operator.EnsureNamespacePolicies(ctx, clientset, egressConfig)
	})

	gpuVendor, err := operator.ParseGPUVendor(gpuVendorFlag)
	if err != nil {
		log.Fatalf("invalid GPU vendor: %v", err)
	}

	capacityCalc, err := operator.NewCapacityCalculator(clientset, namespace, maxAnalyses, nodeLabelSelector)
	if err != nil {
		log.Fatalf("creating capacity calculator: %v", err)
	}
	imageCache := operator.NewImageCacheManager(clientset, namespace, imagePullSecret)
	op, err := operator.NewOperator(operator.OperatorOptions{
		Clientset:           clientset,
		GatewayClient:       gwClient,
		Namespace:           namespace,
		GatewayNamespace:    gatewayNamespace,
		GatewayName:         gatewayName,
		GPUVendor:           gpuVendor,
		CapacityCalc:        capacityCalc,
		ImageCache:          imageCache,
		LoadingServiceName:  loadingServiceName,
		LoadingServicePort:  int32(loadingServicePort),
		LoadingTimeoutMs:    loadingTimeoutMs,
		BaseDomain:          baseDomain,
		ClusterConfigSecret: clusterConfigSecret,
		EgressConfig:        egressConfig,
		UserSuffix:          userSuffix,
	})
	if err != nil {
		log.Fatalf("failed to construct operator: %v", err)
	}

	verifier, err := buildAPIVerifier(context.Background(), apiAuthIssuerURL, apiAuthClientID, apiAuth)
	if err != nil {
		log.Fatalf("%v", err)
	}

	swaggerCfg, err := buildSwaggerAuthConfig(swaggerClientID, swaggerClientSecret, swaggerCookieSecret, apiAuthIssuerURL)
	if err != nil {
		log.Fatalf("%v", err)
	}

	app := NewApp(op, verifier, apiAuthClientID, swaggerCfg)
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
