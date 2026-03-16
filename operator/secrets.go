package operator

import (
	"context"
	"fmt"

	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

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
