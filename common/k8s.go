package common

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubeConfig returns the kubeconfig path from either the KUBECONFIG
// environment variable, the user's home directory, or the --kubeconfig
// flag. Call this function **before** calling flag.Parse().
func KubeConfig() *string {
	var kubeconfig *string

	if cluster := os.Getenv("CLUSTER"); cluster != "" {
		return flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	// Prefer the value in the KUBECONFIG env var.
	// If the value is not set, then check for the HOME directory.
	// If that is not set, then require the user to specify a path.
	if kubeconfigEnv := os.Getenv("KUBECONFIG"); kubeconfigEnv != "" {
		kubeconfig = flag.String("kubeconfig", kubeconfigEnv, "absolute path to the kubeconfig file")
	} else if home := os.Getenv("HOME"); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		// If the home directory doesn't exist, then allow the user to specify a path.
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	return kubeconfig
}

// RESTConfig returns the rest config needed by most k8s-related clients.
// Call this **after** flag.Parse().
func RESTConfig(kubeconfig string) (*rest.Config, error) {
	var (
		err    error
		config *rest.Config
	)

	if cluster := os.Getenv("CLUSTER"); cluster != "" {
		config, err = rest.InClusterConfig()
		if err != nil {
			err = errors.Wrapf(err, "error loading the config inside the cluster")
		}
	} else {
		if kubeconfig == "" {
			return nil, errors.New("either --kubeconfig or CLUSTER=1 must be set")
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			err = errors.Wrapf(err, "error building config from flags using kubeconfig %s", kubeconfig)
		}
	}

	return config, err
}
