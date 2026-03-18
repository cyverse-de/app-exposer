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
		kubeconfig          string
		namespace           string
		port                int
		routingType         string
		ingressClass        string
		gpuVendorFlag       string
		maxAnalyses         int
		nodeLabelSelector   string
		logLevel            string
		basicAuth           bool
		basicAuthUsername   string
		basicAuthPassword   string
		viceBaseURL         string
		clusterConfigSecret string
		imagePullSecret     string
		registryServer      string
		registryUsername    string
		registryPassword    string
		loadingServiceName  string
		loadingServicePort  int
		loadingTimeoutMs    int64
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig (empty for in-cluster)")
	flag.StringVar(&namespace, "namespace", "vice-apps", "Namespace for VICE resources")
	flag.IntVar(&port, "port", 60001, "Listen port")
	flag.StringVar(&routingType, "routing-type", "gateway", "Routing type: gateway, nginx, or tailscale")
	flag.StringVar(&ingressClass, "ingress-class", "nginx", "Ingress class name")
	flag.StringVar(&gpuVendorFlag, "gpu-vendor", "nvidia", "GPU vendor: nvidia or amd")
	flag.IntVar(&maxAnalyses, "max-analyses", 50, "Max concurrent analyses")
	flag.StringVar(&nodeLabelSelector, "node-label-selector", "", "Filter schedulable nodes by label")
	flag.StringVar(&logLevel, "log-level", "info", "Log level")
	flag.BoolVar(&basicAuth, "basic-auth", false, "Enable basic auth for the API")
	flag.StringVar(&basicAuthUsername, "basic-auth-username", "", "Basic auth username (required when --basic-auth is set)")
	flag.StringVar(&basicAuthPassword, "basic-auth-password", "", "Basic auth password (required when --basic-auth is set)")
	flag.StringVar(&viceBaseURL, "vice-base-url", "https://cyverse.run", "Base URL for VICE, stored in the cluster config secret")
	flag.StringVar(&clusterConfigSecret, "cluster-config-secret", "cluster-config-secret", "Name of the K8s Secret holding cluster config (e.g. VICE_BASE_URL)")
	flag.StringVar(&imagePullSecret, "image-pull-secret", "vice-image-pull-secret", "Name of the K8s image pull Secret")
	flag.StringVar(&registryServer, "registry-server", "", "Docker registry server (e.g. harbor.cyverse.org)")
	flag.StringVar(&registryUsername, "registry-username", "", "Docker registry username")
	flag.StringVar(&registryPassword, "registry-password", "", "Docker registry password")
	flag.StringVar(&loadingServiceName, "loading-service-name", "vice-operator-loading", "Name of the loading page service")
	flag.IntVar(&loadingServicePort, "loading-service-port", 80, "Port of the loading page service")
	flag.Int64Var(&loadingTimeoutMs, "loading-timeout-ms", 600000, "Loading page timeout in milliseconds")
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

	// Ensure the cluster config secret exists with the correct VICE_BASE_URL
	// before starting the operator, so vice-proxy containers can reference it
	// as env vars via EnvFrom.
	configCtx, configCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer configCancel()
	if err := operator.EnsureClusterConfigSecret(configCtx, clientset, namespace, clusterConfigSecret, viceBaseURL); err != nil {
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

	rt, err := operator.ParseRoutingType(routingType)
	if err != nil {
		log.Fatalf("invalid routing type: %v", err)
	}

	gpuVendor, err := operator.ParseGPUVendor(gpuVendorFlag)
	if err != nil {
		log.Fatalf("invalid GPU vendor: %v", err)
	}

	// Create gateway client when using gateway routing.
	var gwClient *gatewayclient.GatewayV1Client
	if rt == operator.RoutingGateway {
		gwClient, err = gatewayclient.NewForConfig(config)
		if err != nil {
			log.Fatalf("error creating gateway API client: %v", err)
		}
	}

	capacityCalc := operator.NewCapacityCalculator(clientset, namespace, maxAnalyses, nodeLabelSelector)
	imageCache := operator.NewImageCacheManager(clientset, namespace, imagePullSecret)
	op := operator.NewOperator(clientset, gwClient, namespace, rt, ingressClass, gpuVendor, capacityCalc, imageCache,
		loadingServiceName, int32(loadingServicePort), loadingTimeoutMs)

	app := NewApp(op, basicAuth, basicAuthUsername, basicAuthPassword)
	listenAddr := fmt.Sprintf(":%d", port)
	log.Infof("vice-operator listening on %s (namespace=%s, routing=%s, ingress-class=%s, gpu-vendor=%s, vice-base-url=%s, max-analyses=%d)",
		listenAddr, namespace, routingType, ingressClass, gpuVendorFlag, viceBaseURL, maxAnalyses)

	if err := app.Start(listenAddr); err != nil {
		log.Error(err)
		os.Exit(1)
	}
}
