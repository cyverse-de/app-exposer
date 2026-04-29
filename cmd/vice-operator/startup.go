package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/cyverse-de/app-exposer/operator"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

// buildKubeClients constructs the Kubernetes core and Gateway API clients.
// An empty kubeconfig falls back to in-cluster config.
func buildKubeClients(kubeconfig string) (kubernetes.Interface, gatewayclient.GatewayV1Interface, error) {
	var (
		config *rest.Config
		err    error
	)
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("building kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("creating k8s client: %w", err)
	}

	gwClient, err := gatewayclient.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("creating gateway API client: %w", err)
	}

	return clientset, gwClient, nil
}

// egressInputs gathers the flag-derived values that buildEgressConfig
// needs. Defined locally because the parameter list of buildEgressConfig
// would otherwise be unwieldy; not used elsewhere.
type egressInputs struct {
	namespace             string
	serviceCIDR           string
	blockedCIDRs          []string
	egressPodExceptions   []string
	egressHostExceptions  []string
	egressCIDRExceptions  []string
	ingressPodExceptions  []string
	disableInternetAccess bool
	operatorPodSelector   string
	keycloakBaseURL       string
	disableViceProxyAuth  bool
}

// buildEgressConfig parses and validates the flag-derived inputs that
// drive per-namespace network policies. It auto-detects the service CIDR
// when one wasn't provided, validates all CIDRs, parses pod and ingress
// exceptions, and resolves Keycloak / host-exception hostnames to CIDRs.
// Returns a fully-validated NetworkPolicyConfig ready for
// EnsureNamespacePolicies.
func buildEgressConfig(ctx context.Context, clientset kubernetes.Interface, in egressInputs) (operator.NetworkPolicyConfig, error) {
	serviceCIDR := in.serviceCIDR
	if serviceCIDR == "" {
		detected, err := operator.DetectServiceCIDR(ctx, clientset)
		if err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("auto-detecting service CIDR (set --service-cidr manually): %w", err)
		}
		serviceCIDR = detected
		log.Infof("auto-detected service CIDR: %s", serviceCIDR)
		log.Warnf("auto-detected CIDR uses a /8 prefix; use --service-cidr for a narrower range if needed")
	}

	if _, _, err := net.ParseCIDR(serviceCIDR); err != nil {
		return operator.NetworkPolicyConfig{}, fmt.Errorf("invalid service CIDR %q: %w", serviceCIDR, err)
	}
	for _, cidr := range in.blockedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("invalid --blocked-cidr %q: %w", cidr, err)
		}
	}

	var podExceptions []map[string]string
	for _, exc := range in.egressPodExceptions {
		labels, err := parseSelector(exc)
		if err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("invalid --egress-pod-exception %q: %w", exc, err)
		}
		podExceptions = append(podExceptions, labels)
	}

	// Each ingress exception value is a comma-separated list of key=value
	// pairs. The kubernetes.io/metadata.name pair identifies the source
	// namespace; remaining pairs select pods within it.
	const nsLabelKey = "kubernetes.io/metadata.name"
	var ingressExceptions []operator.IngressException
	for _, exc := range in.ingressPodExceptions {
		labels, err := parseSelector(exc)
		if err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("invalid --ingress-pod-exception %q: %w", exc, err)
		}
		nsName, ok := labels[nsLabelKey]
		if !ok {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("--ingress-pod-exception %q must include %s=<namespace> to identify the source namespace", exc, nsLabelKey)
		}
		delete(labels, nsLabelKey)
		if len(labels) == 0 {
			log.Warnf("--ingress-pod-exception %q has no pod labels; allows ALL pods in namespace %q to reach VICE pods", exc, nsName)
		}
		ingressExceptions = append(ingressExceptions, operator.IngressException{
			NamespaceLabels: map[string]string{nsLabelKey: nsName},
			PodLabels:       labels,
		})
	}
	if len(ingressExceptions) == 0 {
		log.Warn("no --ingress-pod-exception flags provided; ingress policy will only allow vice-operator — external traffic (e.g. Traefik) will be blocked")
	}

	if in.disableInternetAccess {
		log.Info("internet access disabled for analysis pods (--disable-internet-access)")
		if in.keycloakBaseURL == "" && !in.disableViceProxyAuth {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("--disable-internet-access requires --keycloak-base-url (or --disable-vice-proxy-auth); without it vice-proxy cannot reach Keycloak for OIDC auth")
		}
	}

	// Resolve hostnames to IPs for egress CIDR exceptions. Keycloak is
	// included when configured (vice-proxy needs it for OIDC auth);
	// additional hosts come from --egress-host-exception flags.
	var allowedCIDRs []string
	if in.keycloakBaseURL != "" {
		cidrs, err := operator.ResolveHostCIDRs(in.keycloakBaseURL)
		if err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("resolving Keycloak host for egress exception: %w", err)
		}
		allowedCIDRs = append(allowedCIDRs, cidrs...)
		log.Infof("allowing egress to Keycloak IPs: %v", cidrs)
	}
	for _, host := range in.egressHostExceptions {
		// Accept both bare hostnames and URLs with a scheme.
		target := host
		if !strings.Contains(host, "://") {
			target = "https://" + host
		}
		cidrs, err := operator.ResolveHostCIDRs(target)
		if err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("resolving --egress-host-exception %q: %w", host, err)
		}
		allowedCIDRs = append(allowedCIDRs, cidrs...)
		log.Infof("allowing egress to %s: %v", host, cidrs)
	}
	for _, cidr := range in.egressCIDRExceptions {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("invalid --egress-cidr-exception %q: %w", cidr, err)
		}
		allowedCIDRs = append(allowedCIDRs, cidr)
		log.Infof("allowing egress to CIDR %s", cidr)
	}

	operatorLabels := map[string]string{"app": "vice-operator"}
	if in.operatorPodSelector != "" {
		selector, err := parseSelector(in.operatorPodSelector)
		if err != nil {
			return operator.NetworkPolicyConfig{}, fmt.Errorf("invalid --operator-pod-selector: %w", err)
		}
		operatorLabels = selector
	}

	cfg := operator.NetworkPolicyConfig{
		Namespace:         in.namespace,
		OperatorLabels:    operatorLabels,
		ServiceCIDR:       serviceCIDR,
		BlockedCIDRs:      in.blockedCIDRs,
		AllowedCIDRs:      allowedCIDRs,
		PodExceptions:     podExceptions,
		IngressExceptions: ingressExceptions,
		DisableInternet:   in.disableInternetAccess,
	}
	if err := cfg.Validate(); err != nil {
		return operator.NetworkPolicyConfig{}, fmt.Errorf("invalid network policy configuration: %w", err)
	}
	return cfg, nil
}

// buildOIDCProvider performs OIDC discovery against the configured issuer.
// Discovery has a bounded 30s timeout so a slow or down IdP can't hang startup
// forever. Returns (nil, nil) when API auth is disabled — the caller treats
// that as "unauthenticated mode" and skips both the API verifier and the
// Swagger UI login flow.
func buildOIDCProvider(ctx context.Context, issuerURL string, enabled bool) (*oidc.Provider, error) {
	if !enabled {
		log.Warn("API auth disabled (--api-auth=false); all requests are unauthenticated")
		return nil, nil
	}
	discCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(discCtx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("discovering OIDC provider at %q: %w", issuerURL, err)
	}
	return provider, nil
}

// buildAPIVerifier extracts an ID-token verifier from an already-discovered
// OIDC provider. Returns nil when provider is nil (API auth disabled).
func buildAPIVerifier(provider *oidc.Provider, issuerURL, clientID string) *oidc.IDTokenVerifier {
	if provider == nil {
		return nil
	}
	// Keycloak client-credentials tokens use azp instead of aud, so disable
	// the built-in audience check; bearerAuthMiddleware enforces azp/scope.
	log.Infof("OIDC API auth enabled (issuer=%s, expected_client_id=%s)", issuerURL, clientID)
	return provider.Verifier(&oidc.Config{SkipClientIDCheck: true})
}

// buildSwaggerAuthConfig assembles the Swagger UI auth config. When
// swaggerClientID is empty, returns a config with no cookie secret —
// effectively disabling the Swagger login flow. When the cookie secret
// flag is unset, an ephemeral key is generated and the operator warns
// that sessions won't survive restarts.
//
// The OAuth2 Endpoint is taken from the discovered provider so the
// auth/token URLs always match the issuer's metadata.
func buildSwaggerAuthConfig(provider *oidc.Provider, swaggerClientID, swaggerClientSecret, swaggerCookieSecret string) (*SwaggerAuthConfig, error) {
	cfg := &SwaggerAuthConfig{
		ClientID:     swaggerClientID,
		ClientSecret: swaggerClientSecret,
	}
	if swaggerClientID == "" {
		return cfg, nil
	}
	if provider == nil {
		// API auth is disabled, so OIDC discovery didn't run; the Swagger
		// login flow has no endpoints to use and will be effectively disabled.
		log.Warn("Swagger client ID is set but API auth is disabled (--api-auth=false); Swagger UI login will not be available")
		return cfg, nil
	}
	cfg.Endpoint = provider.Endpoint()
	if swaggerCookieSecret != "" {
		cfg.CookieSecret = []byte(swaggerCookieSecret)
	} else {
		generated, err := generateCookieSecret()
		if err != nil {
			return nil, fmt.Errorf("generating cookie secret: %w", err)
		}
		cfg.CookieSecret = generated
		log.Warn("no --swagger-cookie-secret provided; generated an ephemeral key (sessions will not survive restarts)")
	}
	log.Infof("Swagger UI login enabled (client_id=%s)", swaggerClientID)
	return cfg, nil
}
