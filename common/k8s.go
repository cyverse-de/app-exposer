package common

import (
	"github.com/pkg/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RESTConfig returns the rest config needed by most k8s-related clients.
// Call this **after** flag.Parse().
func RESTConfig(kubeconfig string) (*rest.Config, error) {
	var (
		err    error
		config *rest.Config
	)

	if kubeconfig == "" {
		config, err = rest.InClusterConfig()
		if err != nil {
			err = errors.Wrapf(err, "error loading the config inside the cluster")
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			err = errors.Wrapf(err, "error building config from flags using kubeconfig %s", kubeconfig)
		}
	}

	return config, err
}
