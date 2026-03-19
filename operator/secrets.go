package operator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// dockerConfigJSON represents the structure of a Docker config.json file
// used by kubernetes.io/dockerconfigjson secrets.
type dockerConfigJSON struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

// dockerConfigAuth holds credentials for a single Docker registry.
type dockerConfigAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// buildDockerConfigJSON constructs the JSON payload for a
// kubernetes.io/dockerconfigjson secret with a single registry entry.
func buildDockerConfigJSON(server, username, password string) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg := dockerConfigJSON{
		Auths: map[string]dockerConfigAuth{
			server: {
				Username: username,
				Password: password,
				Auth:     auth,
			},
		},
	}
	return json.Marshal(cfg)
}

// EnsureClusterConfigSecret ensures that the named Secret exists in the given
// namespace with the correct cluster config values. It creates the secret if
// missing, merges config keys into existing data (overwriting matches,
// preserving extras), and updates if any values changed.
func EnsureClusterConfigSecret(ctx context.Context, clientset kubernetes.Interface, namespace, secretName string, config map[string]string) error {
	client := clientset.CoreV1().Secrets(namespace)

	// Convert config map to byte map for the Secret data.
	wantData := make(map[string][]byte, len(config))
	for k, v := range config {
		wantData[k] = []byte(v)
	}

	existing, err := client.Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("creating Secret %s with %d keys", secretName, len(config))
		_, err = client.Create(ctx, &apiv1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Data: wantData,
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating Secret %s: %w", secretName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing Secret %s: %w", secretName, err)
	}

	// Secret exists — merge config keys into existing data.
	if existing.Data == nil {
		existing.Data = make(map[string][]byte)
	}

	changed := false
	for k, v := range wantData {
		current, ok := existing.Data[k]
		if !ok || string(current) != string(v) {
			existing.Data[k] = v
			changed = true
		}
	}

	if !changed {
		log.Debugf("Secret %s already has correct values for all config keys", secretName)
		return nil
	}

	log.Infof("updating Secret %s with new config values", secretName)
	_, err = client.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating Secret %s: %w", secretName, err)
	}
	return nil
}

// EnsureImagePullSecret ensures that the named dockerconfigjson Secret exists
// in the given namespace with credentials for the specified registry. It
// creates the secret if missing and updates it if the credentials differ.
func EnsureImagePullSecret(ctx context.Context, clientset kubernetes.Interface, namespace, secretName, server, username, password string) error {
	client := clientset.CoreV1().Secrets(namespace)
	dockerKey := apiv1.DockerConfigJsonKey // ".dockerconfigjson"

	wantData, err := buildDockerConfigJSON(server, username, password)
	if err != nil {
		return fmt.Errorf("building dockerconfigjson for Secret %s: %w", secretName, err)
	}

	existing, err := client.Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("creating image pull Secret %s for registry %s", secretName, server)
		_, err = client.Create(ctx, &apiv1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: apiv1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				dockerKey: wantData,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating Secret %s: %w", secretName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing Secret %s: %w", secretName, err)
	}

	// Secret exists — check whether credentials or type need updating.
	if existing.Type == apiv1.SecretTypeDockerConfigJson && string(existing.Data[dockerKey]) == string(wantData) {
		log.Debugf("image pull Secret %s already has correct credentials", secretName)
		return nil
	}

	log.Infof("updating image pull Secret %s credentials for registry %s", secretName, server)
	existing.Type = apiv1.SecretTypeDockerConfigJson
	if existing.Data == nil {
		existing.Data = make(map[string][]byte)
	}
	existing.Data[dockerKey] = wantData

	_, err = client.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating Secret %s: %w", secretName, err)
	}
	return nil
}
