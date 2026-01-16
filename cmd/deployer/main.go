// Package main provides the entry point for the VICE deployer service.
// The deployer can run in two modes:
//   - standalone: Long-running HTTP server for self-hosted K8s clusters
//   - lambda: AWS Lambda function behind API Gateway for AWS-hosted clusters
package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/deployer"
	"github.com/cyverse-de/go-mod/logging"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var log = common.Log

func main() {
	var (
		mode       = flag.String("mode", "standalone", "Run mode: standalone or lambda")
		port       = flag.Int("port", 8080, "HTTP port (standalone mode only)")
		namespace  = flag.String("namespace", "vice-apps", "Default Kubernetes namespace for deployments")
		kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig file (optional, uses in-cluster config by default)")

		// mTLS options (standalone mode only)
		mtls      = flag.Bool("mtls", false, "Enable mTLS for incoming connections")
		tlsCert   = flag.String("tls-cert", "", "Path to TLS certificate (required if mtls enabled)")
		tlsKey    = flag.String("tls-key", "", "Path to TLS private key (required if mtls enabled)")
		clientCA  = flag.String("client-ca", "", "Path to client CA certificate (required if mtls enabled)")

		logLevel = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	)
	flag.Parse()

	// Configure logging
	logging.SetupLogging(*logLevel)

	log.Infof("VICE Deployer starting in %s mode", *mode)
	log.Infof("Version: %s", deployer.Version)

	// Build Kubernetes client
	k8sClient, err := buildK8sClient(*kubeconfig)
	if err != nil {
		log.Fatalf("failed to build kubernetes client: %v", err)
	}

	// Create deployer
	dep := deployer.New(k8sClient, *namespace)

	switch *mode {
	case "standalone":
		runStandalone(dep, *namespace, *port, *mtls, *tlsCert, *tlsKey, *clientCA)
	case "lambda":
		runLambda(dep, *namespace)
	default:
		log.Fatalf("unknown mode: %s (expected 'standalone' or 'lambda')", *mode)
	}
}

// buildK8sClient creates a Kubernetes client using either the provided kubeconfig
// or in-cluster configuration.
func buildK8sClient(kubeconfigPath string) (kubernetes.Interface, error) {
	var config *rest.Config
	var err error

	// Check if running in Lambda (AWS environment)
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		log.Info("detected AWS Lambda environment, using EKS authentication")
		return buildK8sClientForLambda()
	}

	if kubeconfigPath != "" {
		// Use provided kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
		log.Infof("using kubeconfig from %s", kubeconfigPath)
	} else {
		// Try in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			// Fall back to default kubeconfig location
			home, _ := os.UserHomeDir()
			defaultKubeconfig := home + "/.kube/config"
			if _, statErr := os.Stat(defaultKubeconfig); statErr == nil {
				config, err = clientcmd.BuildConfigFromFlags("", defaultKubeconfig)
				if err != nil {
					return nil, fmt.Errorf("failed to build config from default kubeconfig: %w", err)
				}
				log.Infof("using default kubeconfig from %s", defaultKubeconfig)
			} else {
				return nil, fmt.Errorf("failed to get in-cluster config and no kubeconfig found: %w", err)
			}
		} else {
			log.Info("using in-cluster kubernetes config")
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return clientset, nil
}

// runStandalone runs the deployer as a long-running HTTP server.
func runStandalone(dep *deployer.Deployer, namespace string, port int, mtls bool, tlsCert, tlsKey, clientCA string) {
	e := echo.New()
	e.HideBanner = true

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())

	// Register routes
	handlers := deployer.NewHandlers(dep, namespace)
	handlers.RegisterRoutes(e)

	addr := fmt.Sprintf(":%d", port)

	if mtls {
		// Validate mTLS configuration
		if tlsCert == "" || tlsKey == "" || clientCA == "" {
			log.Fatal("--tls-cert, --tls-key, and --client-ca are required when --mtls is enabled")
		}

		// Load client CA
		caCert, err := os.ReadFile(clientCA)
		if err != nil {
			log.Fatalf("failed to read client CA certificate: %v", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			log.Fatal("failed to parse client CA certificate")
		}

		// Configure TLS
		tlsConfig := &tls.Config{
			ClientCAs:  caCertPool,
			ClientAuth: tls.RequireAndVerifyClientCert,
			MinVersion: tls.VersionTLS12,
		}

		server := &http.Server{
			Addr:      addr,
			Handler:   e,
			TLSConfig: tlsConfig,
		}

		log.Infof("starting HTTPS server with mTLS on %s", addr)
		if err := server.ListenAndServeTLS(tlsCert, tlsKey); err != nil {
			log.Fatalf("server error: %v", err)
		}
	} else {
		log.Infof("starting HTTP server on %s", addr)
		if err := e.Start(addr); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}
}
