package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	wfclientset "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	"github.com/cyverse-de/app-exposer/common"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	var (
		err error
		//cleanupURL = flag.String("cleanup-url", "http://webhook-eventsource-svc.argo-events/batch/cleanup", "The URL to POST to to clean up a workflow")
		namespace = flag.String("namespace", "argo", "The namespace to use when looking up workflows")
	)

	ctx := context.Background()
	kubeconfig := common.KubeConfig()
	flag.Parse()

	config, err := common.RESTConfig(*kubeconfig)
	if err != nil {
		log.Fatal(err)
	}

	wfClientSet := wfclientset.NewForConfigOrDie(config)
	wfAPI := wfClientSet.ArgoprojV1alpha1().Workflows(*namespace)

	// listReqs, err := labels.NewRequirement(
	// 	constants.LabelKeyAppType,
	// 	selection.Equals,
	// 	[]string{constants.LabelValueBatch},
	// )
	// if err != nil {
	// 	log.Fatal(err)
	// }

	wfList, err := wfAPI.List(ctx, v1.ListOptions{})

	for _, wf := range wfList.Items {
		fmt.Printf("%s\t%s\n", wf.Status.Phase, wf.Name)
	}

	// If they're reported as failed, clean them up. Ignore the DE state
	// it gets set before the workflow state is finalized.
}
