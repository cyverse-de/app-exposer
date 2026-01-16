//go:build !lambda
// +build !lambda

package main

import (
	"github.com/cyverse-de/app-exposer/deployer"
	"k8s.io/client-go/kubernetes"
)

// runLambda is a stub that logs an error when Lambda mode is requested
// but the binary was not built with Lambda support.
func runLambda(dep *deployer.Deployer, namespace string) {
	log.Fatal("Lambda mode is not available. Rebuild with: go build -tags lambda")
}

// buildK8sClientForLambda is a stub for non-lambda builds.
func buildK8sClientForLambda() (kubernetes.Interface, error) {
	log.Fatal("Lambda mode is not available. Rebuild with: go build -tags lambda")
	return nil, nil
}
