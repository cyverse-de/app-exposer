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

// EnsureAPIRoute ensures an HTTPRoute exists that routes traffic for the
// vice-operator API through the Gateway. This makes the operator accessible
// at a hostname like "vice-api.localhost" via HAProxy / tailscale serve.
// Unlike EnsureGateway (create-only), this updates the existing route if the
// hostname or backend changed, since the subdomain or service name may change
// between restarts.
func EnsureAPIRoute(
	ctx context.Context,
	gwClient gatewayclient.GatewayV1Interface,
	namespace, gatewayName, hostname, serviceName string,
	servicePort int32,
) error {
	const routeName = "vice-operator-api"

	gwNamespace := gatewayv1.Namespace(namespace)
	gwObjName := gatewayv1.ObjectName(gatewayName)
	svcPort := gatewayv1.PortNumber(servicePort)

	desired := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Namespace: &gwNamespace,
						Name:      gwObjName,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: gatewayv1.ObjectName(serviceName),
									Port: &svcPort,
								},
							},
						},
					},
				},
			},
		},
	}

	client := gwClient.HTTPRoutes(namespace)
	existing, err := client.Get(ctx, routeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("creating API HTTPRoute %s/%s (hostname=%s, backend=%s:%d)", namespace, routeName, hostname, serviceName, servicePort)
		_, err = client.Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating API HTTPRoute %s: %w", routeName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing API HTTPRoute %s: %w", routeName, err)
	}

	// Skip the update if the spec already matches to avoid unnecessary writes.
	if apiRouteMatches(existing, hostname, serviceName, servicePort) {
		log.Debugf("API HTTPRoute %s/%s already up to date", namespace, routeName)
		return nil
	}

	// Copy ResourceVersion and UID from the existing object for optimistic locking.
	desired.ResourceVersion = existing.ResourceVersion
	desired.UID = existing.UID
	log.Infof("updating API HTTPRoute %s/%s (hostname=%s, backend=%s:%d)", namespace, routeName, hostname, serviceName, servicePort)
	_, err = client.Update(ctx, desired, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating API HTTPRoute %s: %w", routeName, err)
	}
	return nil
}

// apiRouteMatches returns true if the existing HTTPRoute already has the
// expected hostname, backend service name, and port.
func apiRouteMatches(route *gatewayv1.HTTPRoute, hostname, serviceName string, servicePort int32) bool {
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != hostname {
		return false
	}
	if len(route.Spec.Rules) != 1 || len(route.Spec.Rules[0].BackendRefs) != 1 {
		return false
	}
	ref := route.Spec.Rules[0].BackendRefs[0].BackendObjectReference
	return string(ref.Name) == serviceName && ref.Port != nil && int32(*ref.Port) == servicePort
}
