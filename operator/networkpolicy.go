package operator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/cyverse-de/app-exposer/operatorclient"
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

	// Extract first octet for /8 range.
	dot := strings.IndexByte(ip, '.')
	if dot < 0 {
		return "", fmt.Errorf("unexpected ClusterIP format: %s", ip)
	}
	cidr := ip[:dot] + ".0.0.0/8"
	return cidr, nil
}

// EnsureEgressPolicies creates or updates the namespace-wide network policies.
// It manages four policies (applied in safe order — allow before deny):
//
//  1. vice-egress-allow — allows external internet (minus blocked CIDRs)
//     plus explicit pod selector exceptions for services that analysis pods
//     need to reach
//  2. vice-operator-egress — allows unrestricted egress for vice-operator
//     pods (trusted operator needs to reach analysis pods, K8s API, etc.)
//  3. vice-default-deny-egress — blocks all egress except DNS (port 53)
//  4. vice-default-deny-ingress — blocks all ingress (per-analysis ingress
//     policies from TransformAddIngressPolicy add specific exceptions)
func EnsureEgressPolicies(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace string,
	serviceCIDR string,
	blockedCIDRs []string,
	podExceptions []map[string]string,
) error {
	npClient := clientset.NetworkingV1().NetworkPolicies(namespace)

	// Apply allow policies before the deny policy. If the deny policy were
	// applied first and the allow policies failed, the namespace would be
	// left with deny-all egress and no exceptions — breaking all running
	// analyses until the operator successfully restarts.

	// Allow external egress + pod exceptions.
	exceptCIDRs := append([]string{serviceCIDR}, blockedCIDRs...)

	allowEgress := []netv1.NetworkPolicyEgressRule{
		{
			To: []netv1.NetworkPolicyPeer{
				{
					IPBlock: &netv1.IPBlock{
						CIDR:   "0.0.0.0/0",
						Except: exceptCIDRs,
					},
				},
			},
		},
	}

	// Add pod selector exceptions from flags. NamespaceSelector: {} means
	// "any namespace", which is needed for cross-namespace pod matching.
	for _, matchLabels := range podExceptions {
		allowEgress = append(allowEgress, netv1.NetworkPolicyEgressRule{
			To: []netv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: matchLabels,
					},
				},
			},
		})
	}

	allowPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-egress-allow",
			Namespace: namespace,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress:      allowEgress,
		},
	}

	if err := upsert(ctx, npClient, "NetworkPolicy", allowPolicy.Name, allowPolicy); err != nil {
		return fmt.Errorf("allow egress policy: %w", err)
	}

	// Unrestricted egress for vice-operator pods (trusted operator).
	operatorPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-operator-egress",
			Namespace: namespace,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "vice-operator"},
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

	// Default deny egress (DNS only) — applied last so the allow policies
	// are already in place before traffic is restricted.
	dnsPort := intstr.FromInt32(53)
	denyPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-default-deny-egress",
			Namespace: namespace,
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

	// Default deny ingress — without this, Kubernetes allows all ingress by
	// default, making per-analysis ingress policies (from TransformAddIngressPolicy)
	// a no-op. With this in place, analysis pods only accept ingress from
	// sources explicitly allowed by per-analysis policies.
	denyIngressPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vice-default-deny-ingress",
			Namespace: namespace,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
		},
	}

	if err := upsert(ctx, npClient, "NetworkPolicy", denyIngressPolicy.Name, denyIngressPolicy); err != nil {
		return fmt.Errorf("deny ingress policy: %w", err)
	}

	return nil
}

// TransformAddIngressPolicy adds a per-analysis ingress NetworkPolicy to
// the bundle that allows vice-operator pods to reach the analysis pod's
// vice-proxy sidecar. The policy is labeled with analysis-id so it's
// cleaned up by deleteAnalysisResources when the analysis exits.
// Returns an error if the deployment is missing the analysis-id label,
// since without the ingress policy vice-operator cannot reach the analysis.
func TransformAddIngressPolicy(bundle *operatorclient.AnalysisBundle) error {
	if bundle == nil || bundle.Deployment == nil {
		return fmt.Errorf("bundle or deployment is nil, cannot create ingress policy")
	}

	analysisID := bundle.Deployment.Labels["analysis-id"]
	if analysisID == "" {
		return fmt.Errorf("deployment %s missing analysis-id label, cannot create ingress policy", bundle.Deployment.Name)
	}

	// Copy labels from the deployment for consistent cleanup.
	labels := maps.Clone(bundle.Deployment.Labels)

	externalID := bundle.Deployment.Name
	bundle.NetworkPolicy = &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   fmt.Sprintf("vice-ingress-%s", externalID),
			Labels: labels,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"analysis-id": analysisID,
				},
			},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
			Ingress: []netv1.NetworkPolicyIngressRule{
				{
					From: []netv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "vice-operator",
								},
							},
						},
					},
				},
			},
		},
	}
	return nil
}

// protocolPtr returns a pointer to a Protocol value.
func protocolPtr(p apiv1.Protocol) *apiv1.Protocol {
	return &p
}
