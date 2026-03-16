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
