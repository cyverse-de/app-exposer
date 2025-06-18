package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	wfclientset "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	"github.com/cyverse-de/app-exposer/constants"

	"github.com/pkg/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var (
		err        error
		kubeconfig *string
		//cleanupURL = flag.String("cleanup-url", "http://webhook-eventsource-svc.argo-events/batch/cleanup", "The URL to POST to to clean up a workflow")
		namespace = flag.String("namespace", "argo", "The namespace to use when looking up workflows")
	)

	ctx := context.Background()

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

	var config *rest.Config
	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			log.Fatal(errors.Wrapf(err, "error building config from flags using kubeconfig %s", *kubeconfig))
		}
	} else {
		// If the home directory doesn't exist and the user doesn't specify a path,
		// then assume that we're running inside a cluster.
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Fatal(errors.Wrapf(err, "error loading the config inside the cluster"))
		}
	}

	wfClientSet := wfclientset.NewForConfigOrDie(config)
	wfAPI := wfClientSet.ArgoprojV1alpha1().Workflows(*namespace)

	listReqs, err := labels.NewRequirement(
		constants.LabelKeyAppType,
		selection.Equals,
		[]string{constants.LabelValueBatch},
	)
	if err != nil {
		log.Fatal(err)
	}

	wfList, err := wfAPI.List(ctx, v1.ListOptions{
		LabelSelector: listReqs.String(),
	})

	for _, wf := range wfList.Items {
		fmt.Printf("%s\t%s\n", wf.Status.String(), wf.Name)
	}

	// If they're reported as failed, clean them up. Ignore the DE state
	// it gets set before the workflow state is finalized.
}
