package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsureClusterConfigSecret(t *testing.T) {
	const (
		ns         = "vice-apps"
		secretName = "cluster-config-secret"
		url        = "https://cyverse.run"
	)

	tests := []struct {
		name      string
		existing  *apiv1.Secret // nil means no pre-existing secret
		wantURL   string
		extraKeys map[string]string // extra keys that should be preserved
	}{
		{
			name:     "creates secret when missing",
			existing: nil,
			wantURL:  url,
		},
		{
			name: "no update when value matches",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       map[string][]byte{"VICE_BASE_URL": []byte(url)},
			},
			wantURL: url,
		},
		{
			name: "updates when value differs",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       map[string][]byte{"VICE_BASE_URL": []byte("https://old.example.com")},
			},
			wantURL: url,
		},
		{
			name: "adds key when missing from existing secret",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       map[string][]byte{"OTHER_KEY": []byte("other-value")},
			},
			wantURL:   url,
			extraKeys: map[string]string{"OTHER_KEY": "other-value"},
		},
		{
			name: "handles existing secret with nil Data map",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       nil,
			},
			wantURL: url,
		},
		{
			name: "preserves extra keys when updating",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data: map[string][]byte{
					"VICE_BASE_URL": []byte("https://old.example.com"),
					"EXTRA":         []byte("keep-me"),
				},
			},
			wantURL:   url,
			extraKeys: map[string]string{"EXTRA": "keep-me"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var clientset *fake.Clientset
			if tt.existing != nil {
				clientset = fake.NewSimpleClientset(tt.existing)
			} else {
				clientset = fake.NewSimpleClientset()
			}

			err := EnsureClusterConfigSecret(context.Background(), clientset, ns, secretName, url)
			require.NoError(t, err)

			// Verify the secret has the correct VICE_BASE_URL.
			secret, err := clientset.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, string(secret.Data["VICE_BASE_URL"]))

			// Verify extra keys are preserved.
			for k, v := range tt.extraKeys {
				assert.Equal(t, v, string(secret.Data[k]), "extra key %q should be preserved", k)
			}
		})
	}
}
