package operator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildDockerConfigJSON(t *testing.T) {
	result, err := buildDockerConfigJSON("harbor.cyverse.org", "robot$vice", "secret-token")
	require.NoError(t, err)

	// Verify it's valid JSON with the expected structure.
	var parsed struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	err = json.Unmarshal(result, &parsed)
	require.NoError(t, err)

	entry, ok := parsed.Auths["harbor.cyverse.org"]
	require.True(t, ok, "should have entry for harbor.cyverse.org")
	assert.Equal(t, "robot$vice", entry.Username)
	assert.Equal(t, "secret-token", entry.Password)

	// Verify auth is base64(username:password).
	decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
	require.NoError(t, err)
	assert.Equal(t, "robot$vice:secret-token", string(decoded))
}

func TestEnsureClusterConfigSecret(t *testing.T) {
	const (
		ns         = "vice-apps"
		secretName = "cluster-config-secret"
	)

	baseConfig := map[string]string{
		"VICE_BASE_URL": "https://cyverse.run",
	}

	multiKeyConfig := map[string]string{
		"VICE_BASE_URL":      "https://cyverse.run",
		"KEYCLOAK_BASE_URL":  "https://keycloak.example.org/auth",
		"KEYCLOAK_REALM":     "cyverse",
		"KEYCLOAK_CLIENT_ID": "vice",
		"DISABLE_AUTH":       "false",
	}

	tests := []struct {
		name      string
		existing  *apiv1.Secret // nil means no pre-existing secret
		config    map[string]string
		wantKeys  map[string]string // keys and values that must be present
		extraKeys map[string]string // extra keys that should be preserved
	}{
		{
			name:     "creates secret when missing",
			existing: nil,
			config:   baseConfig,
			wantKeys: baseConfig,
		},
		{
			name: "no update when value matches",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       map[string][]byte{"VICE_BASE_URL": []byte("https://cyverse.run")},
			},
			config:   baseConfig,
			wantKeys: baseConfig,
		},
		{
			name: "updates when value differs",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       map[string][]byte{"VICE_BASE_URL": []byte("https://old.example.com")},
			},
			config:   baseConfig,
			wantKeys: baseConfig,
		},
		{
			name: "adds key when missing from existing secret",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       map[string][]byte{"OTHER_KEY": []byte("other-value")},
			},
			config:    baseConfig,
			wantKeys:  baseConfig,
			extraKeys: map[string]string{"OTHER_KEY": "other-value"},
		},
		{
			name: "handles existing secret with nil Data map",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data:       nil,
			},
			config:   baseConfig,
			wantKeys: baseConfig,
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
			config:    baseConfig,
			wantKeys:  baseConfig,
			extraKeys: map[string]string{"EXTRA": "keep-me"},
		},
		{
			name:     "creates multi-key secret when missing",
			existing: nil,
			config:   multiKeyConfig,
			wantKeys: multiKeyConfig,
		},
		{
			name: "multi-key update merges correctly",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Data: map[string][]byte{
					"VICE_BASE_URL":     []byte("https://old.example.com"),
					"KEYCLOAK_BASE_URL": []byte("https://keycloak.example.org/auth"),
					"CUSTOM":            []byte("preserved"),
				},
			},
			config:    multiKeyConfig,
			wantKeys:  multiKeyConfig,
			extraKeys: map[string]string{"CUSTOM": "preserved"},
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

			err := EnsureClusterConfigSecret(context.Background(), clientset, ns, secretName, tt.config)
			require.NoError(t, err)

			// Verify the secret has the correct values.
			secret, err := clientset.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
			require.NoError(t, err)

			for k, v := range tt.wantKeys {
				assert.Equal(t, v, string(secret.Data[k]), "key %q should have correct value", k)
			}

			// Verify extra keys are preserved.
			for k, v := range tt.extraKeys {
				assert.Equal(t, v, string(secret.Data[k]), "extra key %q should be preserved", k)
			}
		})
	}
}

func TestEnsureImagePullSecret(t *testing.T) {
	const (
		ns         = "vice-apps"
		secretName = "vice-image-pull-secret"
		server     = "harbor.cyverse.org"
		username   = "robot$vice"
		password   = "secret-token"
	)

	// Pre-build the expected payload for comparison.
	wantData, err := buildDockerConfigJSON(server, username, password)
	require.NoError(t, err)

	dockerKey := apiv1.DockerConfigJsonKey // ".dockerconfigjson"

	tests := []struct {
		name     string
		existing *apiv1.Secret // nil means no pre-existing secret
	}{
		{
			name:     "creates secret when missing",
			existing: nil,
		},
		{
			name: "no update when credentials match",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Type:       apiv1.SecretTypeDockerConfigJson,
				Data:       map[string][]byte{dockerKey: wantData},
			},
		},
		{
			name: "updates when credentials differ",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Type:       apiv1.SecretTypeDockerConfigJson,
				Data:       map[string][]byte{dockerKey: []byte(`{"auths":{"old.registry":{}}}`)},
			},
		},
		{
			name: "handles existing secret with nil Data map",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Type:       apiv1.SecretTypeDockerConfigJson,
				Data:       nil,
			},
		},
		{
			name: "corrects wrong secret type from Opaque to dockerconfigjson",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Type:       apiv1.SecretTypeOpaque,
				Data:       map[string][]byte{dockerKey: wantData},
			},
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

			err := EnsureImagePullSecret(context.Background(), clientset, ns, secretName, server, username, password)
			require.NoError(t, err)

			// Verify the secret exists with correct type and data.
			secret, err := clientset.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, apiv1.SecretTypeDockerConfigJson, secret.Type)
			assert.JSONEq(t, string(wantData), string(secret.Data[dockerKey]))
		})
	}
}
