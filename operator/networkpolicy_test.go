package operator

import (
	"errors"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestBuildAnalysisEgressPolicy(t *testing.T) {
	labels := map[string]string{constants.AnalysisIDLabel: "test-123", constants.AppTypeLabel: "interactive"}

	tests := []struct {
		name string
		cfg  NetworkPolicyConfig
		// wantRuleCount is the total number of egress rules expected.
		wantRuleCount int
		// wantInternetRule signals that a 0.0.0.0/0 internet-access rule
		// should be present; wantExceptCIDRs (when set) asserts the Except
		// list on that rule. Both are independent of wantAllowedCIDRs, which
		// asserts the allow-CIDRs rule's peer list.
		wantInternetRule  bool
		wantExceptCIDRs   []string
		wantAllowedCIDRs  []string
		wantNoExceptEmpty bool
	}{
		{
			name: "normal ServiceCIDR included in except list",
			cfg: NetworkPolicyConfig{
				Namespace:   "vice-apps",
				ServiceCIDR: "10.0.0.0/8",
			},
			wantRuleCount:    1,
			wantInternetRule: true,
			wantExceptCIDRs:  []string{"10.0.0.0/8"},
		},
		{
			name: "empty ServiceCIDR excluded from except list",
			cfg: NetworkPolicyConfig{
				Namespace:   "vice-apps",
				ServiceCIDR: "",
			},
			wantRuleCount:     1,
			wantInternetRule:  true,
			wantNoExceptEmpty: true,
		},
		{
			name: "empty ServiceCIDR with blocked CIDRs",
			cfg: NetworkPolicyConfig{
				Namespace:    "vice-apps",
				ServiceCIDR:  "",
				BlockedCIDRs: []string{"172.16.0.0/12"},
			},
			wantRuleCount:     1,
			wantInternetRule:  true,
			wantExceptCIDRs:   []string{"172.16.0.0/12"},
			wantNoExceptEmpty: true,
		},
		{
			name: "ServiceCIDR combined with blocked CIDRs",
			cfg: NetworkPolicyConfig{
				Namespace:    "vice-apps",
				ServiceCIDR:  "10.0.0.0/8",
				BlockedCIDRs: []string{"172.16.0.0/12"},
			},
			wantRuleCount:    1,
			wantInternetRule: true,
			wantExceptCIDRs:  []string{"10.0.0.0/8", "172.16.0.0/12"},
		},
		{
			name: "DisableInternet omits 0.0.0.0/0 rule",
			cfg: NetworkPolicyConfig{
				Namespace:       "vice-apps",
				ServiceCIDR:     "10.0.0.0/8",
				DisableInternet: true,
			},
			wantRuleCount: 0,
		},
		{
			name: "DisableInternet with allowed CIDRs still creates allow rule",
			cfg: NetworkPolicyConfig{
				Namespace:       "vice-apps",
				ServiceCIDR:     "10.0.0.0/8",
				DisableInternet: true,
				AllowedCIDRs:    []string{"192.168.1.0/24"},
			},
			wantRuleCount:    1,
			wantAllowedCIDRs: []string{"192.168.1.0/24"},
		},
		{
			name: "internet rule and allow rule coexist",
			cfg: NetworkPolicyConfig{
				Namespace:    "vice-apps",
				ServiceCIDR:  "10.0.0.0/8",
				BlockedCIDRs: []string{"172.16.0.0/12"},
				AllowedCIDRs: []string{"192.168.1.0/24", "192.168.2.0/24"},
			},
			wantRuleCount:    2,
			wantInternetRule: true,
			wantExceptCIDRs:  []string{"10.0.0.0/8", "172.16.0.0/12"},
			wantAllowedCIDRs: []string{"192.168.1.0/24", "192.168.2.0/24"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			np := buildAnalysisEgressPolicy("test-123", "vice-apps", labels, tt.cfg)

			require.NotNil(t, np)
			assert.Equal(t, "vice-egress-test-123", np.Name)
			assert.Equal(t, "vice-apps", np.Namespace)
			assert.Equal(t, labels, np.Labels)
			assert.Equal(t, tt.wantRuleCount, len(np.Spec.Egress))

			// Locate the internet-access rule (single peer with CIDR
			// 0.0.0.0/0) and the allow-CIDRs rule (one peer per CIDR,
			// no 0.0.0.0/0). Walking by content rather than index keeps
			// the assertions stable regardless of rule ordering.
			var internetRule, allowRule *netv1.NetworkPolicyEgressRule
			for i := range np.Spec.Egress {
				rule := &np.Spec.Egress[i]
				if isInternetRule(rule) {
					internetRule = rule
				} else if isAllowCIDRsRule(rule) {
					allowRule = rule
				}
			}

			if tt.wantInternetRule {
				require.NotNil(t, internetRule, "expected an internet-access (0.0.0.0/0) rule")
				if tt.wantExceptCIDRs != nil {
					assert.Equal(t, tt.wantExceptCIDRs, internetRule.To[0].IPBlock.Except)
				}
			} else {
				assert.Nil(t, internetRule, "did not expect a 0.0.0.0/0 rule")
			}

			if tt.wantAllowedCIDRs != nil {
				require.NotNil(t, allowRule, "expected an allow-CIDRs rule")
				gotCIDRs := make([]string, len(allowRule.To))
				for i, peer := range allowRule.To {
					require.NotNil(t, peer.IPBlock, "allow rule peers must be IPBlock")
					gotCIDRs[i] = peer.IPBlock.CIDR
				}
				assert.Equal(t, tt.wantAllowedCIDRs, gotCIDRs)
			}

			// Verify no empty string sneaks into any Except list.
			if tt.wantNoExceptEmpty {
				for _, rule := range np.Spec.Egress {
					for _, peer := range rule.To {
						if peer.IPBlock != nil {
							for _, cidr := range peer.IPBlock.Except {
								assert.NotEmpty(t, cidr, "Except list must not contain empty CIDR strings")
							}
						}
					}
				}
			}
		})
	}
}

// isInternetRule reports whether r is the open-internet rule (a single
// IPBlock peer with CIDR 0.0.0.0/0). The Except list may be present.
func isInternetRule(r *netv1.NetworkPolicyEgressRule) bool {
	return len(r.To) == 1 && r.To[0].IPBlock != nil && r.To[0].IPBlock.CIDR == "0.0.0.0/0"
}

// isAllowCIDRsRule reports whether r is an allow-CIDRs rule (every peer
// is an IPBlock with no Except list and no 0.0.0.0/0 CIDR).
func isAllowCIDRsRule(r *netv1.NetworkPolicyEgressRule) bool {
	if len(r.To) == 0 {
		return false
	}
	for _, peer := range r.To {
		if peer.IPBlock == nil {
			return false
		}
		if peer.IPBlock.CIDR == "0.0.0.0/0" {
			return false
		}
		if len(peer.IPBlock.Except) != 0 {
			return false
		}
	}
	return true
}

// TestEnsureNamespacePoliciesHappyPath verifies all three policies land
// in the target namespace with the expected names and PolicyTypes.
func TestEnsureNamespacePoliciesHappyPath(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cfg := NetworkPolicyConfig{
		Namespace:      "vice-apps",
		OperatorLabels: map[string]string{"app": "vice-operator-local"},
	}
	require.NoError(t, EnsureNamespacePolicies(t.Context(), cs, cfg))

	got, err := cs.NetworkingV1().NetworkPolicies("vice-apps").List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, got.Items, 3, "should create exactly three policies")

	byName := map[string]int{}
	for _, np := range got.Items {
		byName[np.Name]++
	}
	assert.Equal(t, 1, byName["vice-operator-egress"], "operator egress policy must exist")
	assert.Equal(t, 1, byName["vice-default-deny-egress"], "deny egress policy must exist")
	assert.Equal(t, 1, byName["vice-default-deny-ingress"], "default-deny ingress policy must exist")
}

// TestEnsureNamespacePoliciesOrderAllowBeforeDeny guards the load-bearing
// invariant: the operator-egress (allow) policy must be created before
// vice-default-deny-egress. If the deny policy landed first and a later
// upsert failed, the namespace would be deny-all-egress with no
// exceptions — breaking every running analysis until the operator
// successfully retried.
func TestEnsureNamespacePoliciesOrderAllowBeforeDeny(t *testing.T) {
	cs := fake.NewSimpleClientset()

	// Record the order in which NetworkPolicies hit the clientset. The
	// fake's reactor chain fires the prepended reactor first for every
	// matching action; returning (false, nil, nil) lets the default
	// tracker handle the actual storage so the test still observes real
	// list/get behavior.
	var createOrder []string
	cs.PrependReactor("create", "networkpolicies", func(action ktesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(ktesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		obj, ok := createAction.GetObject().(metav1.Object)
		if ok {
			createOrder = append(createOrder, obj.GetName())
		}
		return false, nil, nil
	})

	cfg := NetworkPolicyConfig{
		Namespace:      "vice-apps",
		OperatorLabels: map[string]string{"app": "vice-operator-local"},
	}
	require.NoError(t, EnsureNamespacePolicies(t.Context(), cs, cfg))

	require.GreaterOrEqual(t, len(createOrder), 2, "expected at least the two egress policies to be created")
	// Find the indices of the two egress policies and assert the allow
	// policy was created before the deny policy.
	operatorIdx, denyIdx := -1, -1
	for i, name := range createOrder {
		switch name {
		case "vice-operator-egress":
			operatorIdx = i
		case "vice-default-deny-egress":
			denyIdx = i
		}
	}
	require.NotEqual(t, -1, operatorIdx, "operator-egress policy never created")
	require.NotEqual(t, -1, denyIdx, "deny-egress policy never created")
	assert.Less(t, operatorIdx, denyIdx, "allow policy must be created before deny policy")
}

// TestEnsureNamespacePoliciesBailsOnAllowFailure confirms that when the
// first (allow) policy fails to apply, EnsureNamespacePolicies returns
// the error without attempting to create the default-deny policy. A
// partial success that leaves deny-all in place without the allow
// policy would break all cluster traffic.
func TestEnsureNamespacePoliciesBailsOnAllowFailure(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "networkpolicies", func(action ktesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(ktesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		obj, ok := createAction.GetObject().(metav1.Object)
		if !ok {
			return false, nil, nil
		}
		if obj.GetName() == "vice-operator-egress" {
			return true, nil, errors.New("simulated API failure")
		}
		return false, nil, nil
	})

	cfg := NetworkPolicyConfig{
		Namespace:      "vice-apps",
		OperatorLabels: map[string]string{"app": "vice-operator-local"},
	}
	err := EnsureNamespacePolicies(t.Context(), cs, cfg)
	require.Error(t, err)

	// Confirm the deny policy was never created — the allow-before-deny
	// invariant must hold even on partial failure.
	_, err = cs.NetworkingV1().NetworkPolicies("vice-apps").Get(t.Context(), "vice-default-deny-egress", metav1.GetOptions{})
	require.Error(t, err, "deny-egress policy must not exist after an allow-policy failure")
}

func TestNetworkPolicyConfigValidate(t *testing.T) {
	validPodSelector := map[string]string{"app": "traefik"}
	validIngress := IngressException{
		NamespaceLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
		PodLabels:       map[string]string{"app": "traefik"},
	}

	tests := []struct {
		name        string
		cfg         NetworkPolicyConfig
		wantErr     bool
		wantErrPart string
	}{
		{
			name: "happy path — populated selectors validate",
			cfg: NetworkPolicyConfig{
				PodExceptions:     []map[string]string{validPodSelector},
				IngressExceptions: []IngressException{validIngress},
			},
		},
		{
			name: "empty PodExceptions entry rejected",
			cfg: NetworkPolicyConfig{
				PodExceptions: []map[string]string{{}},
			},
			wantErr:     true,
			wantErrPart: "PodExceptions[0]: empty selector matches every pod",
		},
		{
			name: "PodExceptions entry with namespace-label only rejected when selector map is empty",
			cfg: NetworkPolicyConfig{
				PodExceptions: []map[string]string{validPodSelector, {}},
			},
			wantErr:     true,
			wantErrPart: "PodExceptions[1]",
		},
		{
			name: "IngressException with both maps empty rejected",
			cfg: NetworkPolicyConfig{
				IngressExceptions: []IngressException{{}},
			},
			wantErr:     true,
			wantErrPart: "IngressExceptions[0]",
		},
		{
			name: "IngressException with only pod labels allowed",
			cfg: NetworkPolicyConfig{
				IngressExceptions: []IngressException{{PodLabels: map[string]string{"app": "x"}}},
			},
		},
		{
			name: "IngressException with only namespace labels allowed",
			cfg: NetworkPolicyConfig{
				IngressExceptions: []IngressException{{NamespaceLabels: map[string]string{"kubernetes.io/metadata.name": "x"}}},
			},
		},
		{name: "empty config validates", cfg: NetworkPolicyConfig{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrPart != "" {
					assert.Contains(t, err.Error(), tt.wantErrPart)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestIngressExceptionValidate(t *testing.T) {
	tests := []struct {
		name    string
		exc     IngressException
		wantErr bool
	}{
		{"both maps populated", IngressException{NamespaceLabels: map[string]string{"ns": "x"}, PodLabels: map[string]string{"app": "y"}}, false},
		{"namespace labels only", IngressException{NamespaceLabels: map[string]string{"ns": "x"}}, false},
		{"pod labels only", IngressException{PodLabels: map[string]string{"app": "y"}}, false},
		{"both maps empty rejected", IngressException{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.exc.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
