package operator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/cyverse-de/app-exposer/constants"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// DetectServiceCIDR approximates the cluster service CIDR by deriving a broad
// /8 range from the kubernetes API server ClusterIP's first octet (e.g.
// 10.42.0.1 → 10.0.0.0/8). This is intentionally conservative to ensure all
// service IPs are blocked. Use --service-cidr for a precise CIDR if the /8
// blocks legitimate traffic (e.g. pod or node IPs in the same range).
func DetectServiceCIDR(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	svc, err := clientset.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting kubernetes service for CIDR detection: %w", err)
	}

	ip := svc.Spec.ClusterIP
	if ip == "" || ip == "None" {
		return "", errors.New("kubernetes service has no ClusterIP")
	}

	firstOctet, _, ok := strings.Cut(ip, ".")
	if !ok {
		return "", fmt.Errorf("unexpected ClusterIP format: %s", ip)
	}
	cidr := firstOctet + ".0.0.0/8"
	return cidr, nil
}

// IngressException identifies a cross-namespace ingress source by its
// namespace labels and pod labels. Both selectors are used together in the
// NetworkPolicy ingress rule (AND semantics).
type IngressException struct {
	NamespaceLabels map[string]string
	PodLabels       map[string]string
}

// Validate rejects an IngressException that would match every pod
// cluster-wide. Kubernetes treats an empty LabelSelector.MatchLabels
// as "match all", so an exception with both maps empty silently opens
// ingress to everything — the opposite of the operator's intent.
func (e IngressException) Validate() error {
	if len(e.NamespaceLabels) == 0 && len(e.PodLabels) == 0 {
		return errors.New("ingress exception has no namespace or pod labels; would match every pod cluster-wide")
	}
	return nil
}

// NetworkPolicyConfig holds all parameters for network policy management.
type NetworkPolicyConfig struct {
	Namespace         string
	OperatorLabels    map[string]string // Pod selector labels for the operator itself.
	ServiceCIDR       string
	BlockedCIDRs      []string            // CIDRs to block in egress (in addition to ServiceCIDR).
	AllowedCIDRs      []string            // Explicit CIDR exceptions always allowed (e.g. Keycloak IPs).
	PodExceptions     []map[string]string // Pod selector labels for egress exceptions.
	IngressExceptions []IngressException  // Cross-namespace ingress sources.
	DisableInternet   bool                // Block analysis pods from public internet.
}

// Validate catches configurations whose selectors would produce
// cluster-wide "allow everything" behavior due to Kubernetes' empty-
// selector-means-match-all semantics. Intended to run once at startup
// against the config built from operator CLI flags.
func (c NetworkPolicyConfig) Validate() error {
	for i, sel := range c.PodExceptions {
		if len(sel) == 0 {
			return fmt.Errorf("PodExceptions[%d]: empty selector matches every pod", i)
		}
	}
	for i, exc := range c.IngressExceptions {
		if err := exc.Validate(); err != nil {
			return fmt.Errorf("IngressExceptions[%d]: %w", i, err)
		}
	}
	return nil
}

// ResolveHostCIDRs resolves a URL's hostname to /32 CIDRs for use in
// NetworkPolicy IPBlock rules. If the host is already an IP address, it is
// returned directly without DNS resolution. Returns all IPs as /32 entries.
func ResolveHostCIDRs(rawURL string) ([]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL %q: %w", rawURL, err)
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("no hostname in URL %q", rawURL)
	}

	// If it's already an IP, return it directly.
	if ip := net.ParseIP(host); ip != nil {
		return []string{ipToCIDR(ip)}, nil
	}

	ips, err := net.LookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IPs found for %q", host)
	}

	cidrs := make([]string, 0, len(ips))
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		cidrs = append(cidrs, ipToCIDR(ip))
	}
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("no valid IPs found for %q", host)
	}
	return cidrs, nil
}

// EnsureNamespacePolicies creates or updates the namespace-wide network policies
// applied at startup. These are the baseline restrictions that apply to all pods
// in the namespace. Per-analysis egress allow policies are created separately
// during HandleLaunch via buildAnalysisEgressPolicy.
//
// Policies managed (applied in safe order — allow before deny):
//
//  1. vice-operator-egress — allows unrestricted egress for vice-operator
//     pods (trusted operator needs to reach analysis pods, K8s API, etc.)
//  2. vice-default-deny-egress — blocks all egress except DNS (port 53)
//  3. vice-default-deny-ingress — blocks all ingress except from
//     vice-operator (same namespace) and configured ingress exceptions
//     (e.g. Traefik from another namespace)
func EnsureNamespacePolicies(
	ctx context.Context,
	clientset kubernetes.Interface,
	cfg NetworkPolicyConfig,
) error {
	npClient := clientset.NetworkingV1().NetworkPolicies(cfg.Namespace)

	// Apply allow policies before the deny policy. If the deny policy were
	// applied first and the allow policies failed, the namespace would be
	// left with deny-all egress and no exceptions — breaking all running
	// analyses until the operator successfully restarts.

	// Unrestricted egress for vice-operator pods (trusted operator).
	operatorPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-operator-egress",
			Namespace: cfg.Namespace,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: cfg.OperatorLabels,
			},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress: []netv1.NetworkPolicyEgressRule{
				{}, // allow all egress
			},
		},
	}

	if err := upsert(ctx, npClient, "NetworkPolicy", operatorPolicy.Name, operatorPolicy); err != nil {
		return fmt.Errorf("operator egress policy: %w", err)
	}

	// Default deny egress (DNS only) — applied after the operator policy so
	// the operator's unrestricted egress is already in place.
	dnsPort := intstr.FromInt32(53)
	denyPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-default-deny-egress",
			Namespace: cfg.Namespace,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress: []netv1.NetworkPolicyEgressRule{
				{
					Ports: []netv1.NetworkPolicyPort{
						{Port: &dnsPort, Protocol: protocolPtr(apiv1.ProtocolTCP)},
						{Port: &dnsPort, Protocol: protocolPtr(apiv1.ProtocolUDP)},
					},
				},
			},
		},
	}

	if err := upsert(ctx, npClient, "NetworkPolicy", denyPolicy.Name, denyPolicy); err != nil {
		return fmt.Errorf("deny egress policy: %w", err)
	}

	// Ingress policy — restricts ingress to the namespace. Always allows
	// vice-operator (same namespace) to reach all pods. Additional ingress
	// sources (e.g. Traefik from another namespace) come from the
	// --ingress-pod-exception flags.
	ingressRules := []netv1.NetworkPolicyIngressRule{
		{
			// Allow vice-operator (same namespace) to reach all pods.
			From: []netv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: cfg.OperatorLabels,
					},
				},
			},
		},
	}

	// Add cross-namespace ingress exceptions (e.g. Traefik).
	// When exc.PodLabels is empty the PodSelector matches all pods in the
	// selected namespace, which is intentional for namespace-wide exceptions.
	for _, exc := range cfg.IngressExceptions {
		ingressRules = append(ingressRules, netv1.NetworkPolicyIngressRule{
			From: []netv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: exc.NamespaceLabels,
					},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: exc.PodLabels,
					},
				},
			},
		})
	}

	ingressPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-default-deny-ingress",
			Namespace: cfg.Namespace,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
			Ingress:     ingressRules,
		},
	}

	if err := upsert(ctx, npClient, "NetworkPolicy", ingressPolicy.Name, ingressPolicy); err != nil {
		return fmt.Errorf("ingress policy: %w", err)
	}

	return nil
}

// buildAnalysisEgressPolicy creates a NetworkPolicy that allows egress for a
// specific analysis's pods. The policy uses the analysis-id label as the pod
// selector so it only applies to that analysis.
//
// When DisableInternet is false, the policy allows all external IPs (minus
// blocked CIDRs). When true, only explicit CIDR exceptions (e.g. Keycloak)
// and pod selector exceptions are allowed.
//
// The returned policy carries all the bundle's labels so it is cleaned up
// by deleteAnalysisResources along with the other per-analysis resources.
func buildAnalysisEgressPolicy(
	analysisID string,
	namespace string,
	bundleLabels map[string]string,
	cfg NetworkPolicyConfig,
) *netv1.NetworkPolicy {
	// Rules may be empty when DisableInternet is true and no CIDR/pod
	// exceptions are configured. An empty Egress list with PolicyType Egress
	// denies all egress, but the namespace-wide DNS-allow policy still
	// applies (K8s NetworkPolicy is union-based across policies).
	var rules []netv1.NetworkPolicyEgressRule

	// When internet access is enabled, allow all external IPs except the
	// service CIDR and any additional blocked CIDRs.
	if !cfg.DisableInternet {
		// Build the except list defensively: skip empty ServiceCIDR (which
		// would be rejected by the K8s API as an invalid CIDR).
		var exceptCIDRs []string
		if cfg.ServiceCIDR != "" {
			exceptCIDRs = append(exceptCIDRs, cfg.ServiceCIDR)
		}
		exceptCIDRs = append(exceptCIDRs, cfg.BlockedCIDRs...)
		rules = append(rules, netv1.NetworkPolicyEgressRule{
			To: []netv1.NetworkPolicyPeer{
				{
					IPBlock: &netv1.IPBlock{
						CIDR:   "0.0.0.0/0",
						Except: exceptCIDRs,
					},
				},
			},
		})
	}

	// Always allow explicit CIDR exceptions (e.g. Keycloak IPs). Vice-proxy
	// needs to reach Keycloak for OIDC auth regardless of the internet
	// access setting. All allowed CIDRs share a single rule.
	if len(cfg.AllowedCIDRs) > 0 {
		peers := make([]netv1.NetworkPolicyPeer, len(cfg.AllowedCIDRs))
		for i, cidr := range cfg.AllowedCIDRs {
			peers[i] = netv1.NetworkPolicyPeer{
				IPBlock: &netv1.IPBlock{CIDR: cidr},
			}
		}
		rules = append(rules, netv1.NetworkPolicyEgressRule{To: peers})
	}

	// Allow egress to specific pods (e.g. internal services that analyses
	// need). NamespaceSelector: {} means "any namespace". All pod exceptions
	// share a single rule (peers have OR semantics).
	if len(cfg.PodExceptions) > 0 {
		peers := make([]netv1.NetworkPolicyPeer, len(cfg.PodExceptions))
		for i, matchLabels := range cfg.PodExceptions {
			peers[i] = netv1.NetworkPolicyPeer{
				NamespaceSelector: &metav1.LabelSelector{},
				PodSelector: &metav1.LabelSelector{
					MatchLabels: matchLabels,
				},
			}
		}
		rules = append(rules, netv1.NetworkPolicyEgressRule{To: peers})
	}

	return &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-egress-" + analysisID,
			Namespace: namespace,
			Labels:    bundleLabels,
		},
		Spec: netv1.NetworkPolicySpec{
			// Target only this analysis's pods.
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{constants.AnalysisIDLabel: analysisID},
			},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress:      rules,
		},
	}
}

// ipToCIDR returns a single-host CIDR string: /32 for IPv4, /128 for IPv6.
func ipToCIDR(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

// protocolPtr returns a pointer to a Protocol value.
func protocolPtr(p apiv1.Protocol) *apiv1.Protocol {
	return &p
}
