//go:build lambda
// +build lambda

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/cyverse-de/app-exposer/deployer"
	"github.com/cyverse-de/app-exposer/vicetypes"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// LambdaHandler handles API Gateway requests in Lambda mode.
type LambdaHandler struct {
	deployer  *deployer.Deployer
	namespace string
}

// NewLambdaHandler creates a new Lambda handler.
func NewLambdaHandler(dep *deployer.Deployer, namespace string) *LambdaHandler {
	return &LambdaHandler{
		deployer:  dep,
		namespace: namespace,
	}
}

// Handle processes incoming API Gateway requests.
func (h *LambdaHandler) Handle(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	log.Infof("handling %s %s", req.HTTPMethod, req.Path)

	// Route based on path and method
	switch {
	case req.HTTPMethod == "POST" && req.Path == "/api/v1/deployments":
		return h.createDeployment(ctx, req)

	case req.HTTPMethod == "DELETE" && strings.HasPrefix(req.Path, "/api/v1/deployments/"):
		externalID := extractPathParam(req.Path, "/api/v1/deployments/", "/")
		return h.deleteDeployment(ctx, externalID, req.QueryStringParameters["namespace"])

	case req.HTTPMethod == "GET" && strings.HasSuffix(req.Path, "/status"):
		externalID := extractPathParam(req.Path, "/api/v1/deployments/", "/status")
		return h.getStatus(ctx, externalID, req.QueryStringParameters["namespace"])

	case req.HTTPMethod == "GET" && strings.HasSuffix(req.Path, "/url-ready"):
		externalID := extractPathParam(req.Path, "/api/v1/deployments/", "/url-ready")
		return h.checkURLReady(ctx, externalID, req.QueryStringParameters["namespace"])

	case req.HTTPMethod == "GET" && strings.HasSuffix(req.Path, "/logs"):
		externalID := extractPathParam(req.Path, "/api/v1/deployments/", "/logs")
		return h.getLogs(ctx, externalID, req)

	case req.HTTPMethod == "GET" && (req.Path == "/api/v1/health" || req.Path == "/health"):
		return h.health(ctx)

	default:
		return jsonResponse(http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("unknown route: %s %s", req.HTTPMethod, req.Path),
		})
	}
}

func (h *LambdaHandler) createDeployment(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var spec vicetypes.VICEDeploymentSpec
	if err := json.Unmarshal([]byte(req.Body), &spec); err != nil {
		return jsonResponse(http.StatusBadRequest, vicetypes.DeploymentResponse{
			Status: "error",
			Error:  "invalid request body: " + err.Error(),
		})
	}

	if spec.Metadata.ExternalID == "" {
		return jsonResponse(http.StatusBadRequest, vicetypes.DeploymentResponse{
			Status: "error",
			Error:  "external_id is required in metadata",
		})
	}

	resp, err := h.deployer.CreateDeployment(ctx, &spec)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, resp)
	}

	return jsonResponse(http.StatusCreated, resp)
}

func (h *LambdaHandler) deleteDeployment(ctx context.Context, externalID, namespace string) (events.APIGatewayProxyResponse, error) {
	if namespace == "" {
		namespace = h.namespace
	}

	resp, err := h.deployer.DeleteDeployment(ctx, externalID, namespace)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, resp)
	}

	return jsonResponse(http.StatusOK, resp)
}

func (h *LambdaHandler) getStatus(ctx context.Context, externalID, namespace string) (events.APIGatewayProxyResponse, error) {
	if namespace == "" {
		namespace = h.namespace
	}

	status, err := h.deployer.GetStatus(ctx, externalID, namespace)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	return jsonResponse(http.StatusOK, status)
}

func (h *LambdaHandler) checkURLReady(ctx context.Context, externalID, namespace string) (events.APIGatewayProxyResponse, error) {
	if namespace == "" {
		namespace = h.namespace
	}

	ready, err := h.deployer.CheckURLReady(ctx, externalID, namespace)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	return jsonResponse(http.StatusOK, ready)
}

func (h *LambdaHandler) getLogs(ctx context.Context, externalID string, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	namespace := req.QueryStringParameters["namespace"]
	if namespace == "" {
		namespace = h.namespace
	}

	logsReq := &vicetypes.LogsRequest{
		Container: req.QueryStringParameters["container"],
		Previous:  req.QueryStringParameters["previous"] == "true",
	}

	logs, err := h.deployer.GetLogs(ctx, externalID, namespace, logsReq)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, vicetypes.LogsResponse{
			Error: err.Error(),
		})
	}

	return jsonResponse(http.StatusOK, logs)
}

func (h *LambdaHandler) health(ctx context.Context) (events.APIGatewayProxyResponse, error) {
	health := h.deployer.Health(ctx)
	statusCode := http.StatusOK
	if health.Status != "healthy" {
		statusCode = http.StatusServiceUnavailable
	}
	return jsonResponse(statusCode, health)
}

// Helper functions

func extractPathParam(path, prefix, suffix string) string {
	path = strings.TrimPrefix(path, prefix)
	if suffix != "" {
		idx := strings.Index(path, suffix)
		if idx > 0 {
			path = path[:idx]
		}
	}
	return path
}

func jsonResponse(statusCode int, body interface{}) (events.APIGatewayProxyResponse, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       `{"error":"failed to marshal response"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: statusCode,
		Body:       string(jsonBody),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}

// runLambda starts the Lambda handler.
func runLambda(dep *deployer.Deployer, namespace string) {
	handler := NewLambdaHandler(dep, namespace)
	log.Info("starting Lambda handler")
	lambda.Start(handler.Handle)
}

// buildK8sClientForLambda creates a Kubernetes client for AWS Lambda.
// It retrieves the kubeconfig from AWS Secrets Manager.
func buildK8sClientForLambda() (kubernetes.Interface, error) {
	kubeconfigSecret := os.Getenv("KUBECONFIG_SECRET")
	if kubeconfigSecret == "" {
		return nil, fmt.Errorf("KUBECONFIG_SECRET environment variable not set")
	}

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Get secret from Secrets Manager
	client := secretsmanager.NewFromConfig(cfg)
	result, err := client.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{
		SecretId: &kubeconfigSecret,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig from Secrets Manager: %w", err)
	}

	var kubeconfigData []byte
	if result.SecretString != nil {
		kubeconfigData = []byte(*result.SecretString)
	} else if result.SecretBinary != nil {
		kubeconfigData = result.SecretBinary
	} else {
		return nil, fmt.Errorf("secret %s has no value", kubeconfigSecret)
	}

	// Check if base64 encoded
	if decoded, err := base64.StdEncoding.DecodeString(string(kubeconfigData)); err == nil {
		kubeconfigData = decoded
	}

	// Build client from kubeconfig
	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get REST config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return clientset, nil
}
