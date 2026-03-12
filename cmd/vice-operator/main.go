// Package main implements the vice-operator binary, a minimal K8s operator
// that receives pre-built resource bundles from app-exposer and applies them
// to the local cluster.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/operator"
	"github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var log = common.Log

func main() {
	var (
		kubeconfig        string
		namespace         string
		port              int
		routingType       string
		ingressClass      string
		maxAnalyses       int
		nodeLabelSelector string
		logLevel          string
		basicAuth         bool
		basicAuthUsername string
		basicAuthPassword string
	)

	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig (empty for in-cluster)")
	flag.StringVar(&namespace, "namespace", "vice-apps", "Namespace for VICE resources")
	flag.IntVar(&port, "port", 60001, "Listen port")
	flag.StringVar(&routingType, "routing-type", "nginx", "Routing type: nginx or tailscale")
	flag.StringVar(&ingressClass, "ingress-class", "nginx", "Ingress class name")
	flag.IntVar(&maxAnalyses, "max-analyses", 50, "Max concurrent analyses")
	flag.StringVar(&nodeLabelSelector, "node-label-selector", "", "Filter schedulable nodes by label")
	flag.StringVar(&logLevel, "log-level", "info", "Log level")
	flag.BoolVar(&basicAuth, "basic-auth", false, "Enable basic auth for the API")
	flag.StringVar(&basicAuthUsername, "basic-auth-username", "", "Basic auth username (required when --basic-auth is set)")
	flag.StringVar(&basicAuthPassword, "basic-auth-password", "", "Basic auth password (required when --basic-auth is set)")
	flag.Parse()

	// Validate basic auth flags.
	if basicAuth && (basicAuthUsername == "" || basicAuthPassword == "") {
		log.Fatal("--basic-auth-username and --basic-auth-password are required when --basic-auth is enabled")
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

	rt := operator.RoutingNginx
	if routingType == "tailscale" {
		rt = operator.RoutingTailscale
	}

	capacityCalc := operator.NewCapacityCalculator(clientset, namespace, maxAnalyses, nodeLabelSelector)
	op := operator.NewOperator(clientset, namespace, rt, ingressClass, capacityCalc)

	app := NewApp(op, basicAuth, basicAuthUsername, basicAuthPassword)
	listenAddr := fmt.Sprintf(":%d", port)
	log.Infof("vice-operator listening on %s (namespace=%s, routing=%s, ingress-class=%s, max-analyses=%d)",
		listenAddr, namespace, routingType, ingressClass, maxAnalyses)

	if err := app.Start(listenAddr); err != nil {
		log.Error(err)
		os.Exit(1)
	}
}
