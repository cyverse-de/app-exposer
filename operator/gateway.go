package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

// EnsureGateway ensures the Gateway resource exists in the given namespace
// with the expected configuration. Creates it if missing; does not update
// an existing Gateway to avoid disrupting live traffic.
func EnsureGateway(
	ctx context.Context,
	gwClient gatewayclient.GatewayV1Interface,
	namespace string,
	gatewayName string,
	gatewayClassName string,
	entrypointPort int32,
) error {
	_, err := gwClient.Gateways(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err == nil {
		log.Debugf("Gateway %s/%s already exists", namespace, gatewayName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking for existing Gateway %s/%s: %w", namespace, gatewayName, err)
	}

	className := gatewayv1.ObjectName(gatewayClassName)
	log.Infof("creating Gateway %s/%s (class=%s, port=%d)", namespace, gatewayName, gatewayClassName, entrypointPort)
	_, err = gwClient.Gateways(namespace).Create(ctx, &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gatewayName,
			Namespace: namespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: className,
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Protocol: gatewayv1.HTTPProtocolType,
					Port:     gatewayv1.PortNumber(entrypointPort),
					AllowedRoutes: &gatewayv1.AllowedRoutes{
						Namespaces: &gatewayv1.RouteNamespaces{
							From: routeNamespacesAll(),
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating Gateway %s/%s: %w", namespace, gatewayName, err)
	}
	return nil
}

// routeNamespacesAll returns a pointer to the "All" FromNamespaces constant.
func routeNamespacesAll() *gatewayv1.FromNamespaces {
	all := gatewayv1.NamespacesFromAll
	return &all
}

// EnsureCORSMiddleware ensures the Traefik CORS Middleware CRD resource exists
// in the given namespace. Uses the REST client directly since the Middleware CRD
// is a Traefik-specific type not in the standard typed client.
func EnsureCORSMiddleware(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	const (
		middlewareName = "vice-cors-headers"
		apiGroup       = "traefik.io"
		apiVersion     = "v1alpha1"
		apiResource    = "middlewares"
	)

	// Build the Middleware object as a plain map (no typed struct needed).
	obj := map[string]any{
		"apiVersion": apiGroup + "/" + apiVersion,
		"kind":       "Middleware",
		"metadata": map[string]any{
			"name":      middlewareName,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"headers": map[string]any{
				"accessControlAllowMethods":    []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
				"accessControlAllowOriginList": []string{"*"},
				"accessControlAllowHeaders":    []string{"*"},
				"accessControlMaxAge":          600,
			},
		},
	}

	log.Debugf("ensuring CORS middleware %s/%s exists", namespace, middlewareName)

	// Check if the middleware already exists via the REST client.
	restClient := clientset.CoreV1().RESTClient()
	getErr := restClient.
		Get().
		AbsPath("/apis", apiGroup, apiVersion, "namespaces", namespace, apiResource, middlewareName).
		Do(ctx).
		Error()

	if getErr == nil {
		log.Debugf("CORS middleware %s/%s already exists", namespace, middlewareName)
		return nil
	}

	// Only proceed with creation if the error is a genuine "not found".
	var statusErr *apierrors.StatusError
	if !errors.As(getErr, &statusErr) || statusErr.ErrStatus.Reason != metav1.StatusReasonNotFound {
		return fmt.Errorf("checking for existing CORS middleware %s/%s: %w", namespace, middlewareName, getErr)
	}

	log.Infof("creating CORS middleware %s/%s", namespace, middlewareName)

	bodyBytes, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshaling CORS middleware: %w", err)
	}

	createErr := restClient.
		Post().
		AbsPath("/apis", apiGroup, apiVersion, "namespaces", namespace, apiResource).
		Body(bodyBytes).
		SetHeader("Content-Type", "application/json").
		Do(ctx).
		Error()

	if createErr != nil {
		return fmt.Errorf("creating CORS middleware %s/%s: %w", namespace, middlewareName, createErr)
	}
	return nil
}
