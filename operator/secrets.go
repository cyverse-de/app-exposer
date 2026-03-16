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
// namespace with the correct VICE_BASE_URL value. It creates the secret if
// missing, updates only the VICE_BASE_URL key if it differs, and preserves
// any other keys already present.
func EnsureClusterConfigSecret(ctx context.Context, clientset kubernetes.Interface, namespace, secretName, viceBaseURL string) error {
	client := clientset.CoreV1().Secrets(namespace)

	existing, err := client.Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("creating Secret %s with VICE_BASE_URL=%s", secretName, viceBaseURL)
		_, err = client.Create(ctx, &apiv1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"VICE_BASE_URL": []byte(viceBaseURL),
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

	// Secret exists — check whether VICE_BASE_URL needs updating.
	current, ok := existing.Data["VICE_BASE_URL"]
	if ok && string(current) == viceBaseURL {
		log.Debugf("Secret %s already has correct VICE_BASE_URL", secretName)
		return nil
	}

	if ok {
		log.Infof("updating Secret %s VICE_BASE_URL from %q to %q", secretName, string(current), viceBaseURL)
	} else {
		log.Infof("adding missing VICE_BASE_URL key to Secret %s", secretName)
	}

	if existing.Data == nil {
		existing.Data = make(map[string][]byte)
	}
	existing.Data["VICE_BASE_URL"] = []byte(viceBaseURL)

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
