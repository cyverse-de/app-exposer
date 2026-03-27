package operator

import (
	"context"
	"fmt"
	"strings"

	netv1 "k8s.io/api/networking/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/cyverse-de/app-exposer/operatorclient"
)

// DetectServiceCIDR auto-detects the cluster service CIDR by reading the
// kubernetes API server's ClusterIP and deriving a /8 range from its first
// octet (e.g. 10.42.0.1 → 10.0.0.0/8).
func DetectServiceCIDR(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	svc, err := clientset.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting kubernetes service for CIDR detection: %w", err)
	}

	ip := svc.Spec.ClusterIP
	if ip == "" || ip == "None" {
		return "", fmt.Errorf("kubernetes service has no ClusterIP")
	}

	// Extract first octet for /8 range.
	dot := strings.IndexByte(ip, '.')
	if dot < 0 {
		return "", fmt.Errorf("unexpected ClusterIP format: %s", ip)
	}
	cidr := ip[:dot] + ".0.0.0/8"
	return cidr, nil
}

// EnsureEgressPolicies creates or updates the namespace-wide egress network
// policies. It manages three policies:
//
//  1. vice-default-deny-egress — blocks all egress except DNS (port 53)
//  2. vice-egress-allow — allows external internet (minus blocked CIDRs)
//     plus explicit pod selector exceptions for services that analysis pods
//     need to reach
//  3. vice-operator-egress — allows unrestricted egress for vice-operator
//     pods (trusted operator needs to reach analysis pods, K8s API, etc.)
func EnsureEgressPolicies(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace string,
	serviceCIDR string,
	blockedCIDRs []string,
	podExceptions []map[string]string,
) error {
	npClient := clientset.NetworkingV1().NetworkPolicies(namespace)

	// Policy 1: default deny egress (DNS only).
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

	// Policy 2: allow external egress + pod exceptions.
	exceptCIDRs := []string{serviceCIDR}
	exceptCIDRs = append(exceptCIDRs, blockedCIDRs...)

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

	// Add pod selector exceptions from flags.
	for _, labels := range podExceptions {
		allowEgress = append(allowEgress, netv1.NetworkPolicyEgressRule{
			To: []netv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: labels,
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

	// Policy 3: unrestricted egress for vice-operator pods.
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

	return nil
}

// TransformAddIngressPolicy adds a per-analysis ingress NetworkPolicy to
// the bundle that allows vice-operator pods to reach the analysis pod's
// vice-proxy sidecar. The policy is labeled with analysis-id so it's
// cleaned up by deleteAnalysisResources when the analysis exits.
func TransformAddIngressPolicy(bundle *operatorclient.AnalysisBundle) {
	if bundle == nil || bundle.Deployment == nil {
		return
	}

	analysisID := bundle.Deployment.Labels["analysis-id"]
	if analysisID == "" {
		return
	}

	// Copy labels from the deployment for consistent cleanup.
	labels := make(map[string]string)
	for k, v := range bundle.Deployment.Labels {
		labels[k] = v
	}

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
}

// protocolPtr returns a pointer to a Protocol value.
func protocolPtr(p apiv1.Protocol) *apiv1.Protocol {
	return &p
}
