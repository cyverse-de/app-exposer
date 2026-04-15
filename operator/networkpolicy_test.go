package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAnalysisEgressPolicy(t *testing.T) {
	labels := map[string]string{"analysis-id": "test-123", "app-type": "interactive"}

	tests := []struct {
		name             string
		cfg              NetworkPolicyConfig
		wantRuleCount    int
		wantExceptCIDRs  []string // expected Except list on the first rule (if present)
		wantNoExceptEmpty bool    // if true, verify no empty string in Except
	}{
		{
			name: "normal ServiceCIDR included in except list",
			cfg: NetworkPolicyConfig{
				Namespace:  "vice-apps",
				ServiceCIDR: "10.0.0.0/8",
			},
			wantRuleCount:   1,
			wantExceptCIDRs: []string{"10.0.0.0/8"},
		},
		{
			name: "empty ServiceCIDR excluded from except list",
			cfg: NetworkPolicyConfig{
				Namespace:   "vice-apps",
				ServiceCIDR: "",
			},
			wantRuleCount:     1,
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
			wantRuleCount:   1,
			wantExceptCIDRs: []string{"10.0.0.0/8", "172.16.0.0/12"},
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
			wantRuleCount: 1,
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

			// Check the except list on the internet-access rule (first rule
			// when DisableInternet is false).
			if tt.wantExceptCIDRs != nil && len(np.Spec.Egress) > 0 {
				rule := np.Spec.Egress[0]
				require.Len(t, rule.To, 1)
				require.NotNil(t, rule.To[0].IPBlock)
				assert.Equal(t, "0.0.0.0/0", rule.To[0].IPBlock.CIDR)
				assert.Equal(t, tt.wantExceptCIDRs, rule.To[0].IPBlock.Except)
			}

			// Verify no empty string sneaks into the except list.
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
