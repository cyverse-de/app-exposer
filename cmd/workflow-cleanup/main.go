package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	wfclientset "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/httphandlers"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

func analysisStatus(appExposerURL, externalID string) (constants.AnalysisStatus, error) {
	statusURL, err := url.JoinPath(appExposerURL, "info/analysis/status/by/external-id", externalID)
	if err != nil {
		return "", err
	}
	resp, err := http.Get(statusURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var statusParsed httphandlers.AnalysisStatus
	if err = json.Unmarshal(bodyBytes, &statusParsed); err != nil {
		return "", err
	}
	return constants.AnalysisStatus(statusParsed.Status), nil
}

func sendStatus(statusURL, externalID, message string, state constants.AnalysisStatus) error {
	body := map[string]string{
		"job_uuid": externalID,
		"hostname": "batch",
		"message":  message,
		"state":    string(state),
	}

	b, err := json.Marshal(&body)
	if err != nil {
		return err
	}

	resp, err := http.Post(statusURL, "application/json", bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 || resp.StatusCode < 200 {
		return fmt.Errorf("status code %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func sendCleanupMessage(cleanUpURL, externalID string) error {
	body := map[string]string{
		"uuid": externalID,
	}

	b, err := json.Marshal(&body)
	if err != nil {
		return err
	}

	resp, err := http.Post(cleanUpURL, "application/json", bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 || resp.StatusCode < 200 {
		return fmt.Errorf("status code %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func workflowStatusToDEStatus(workflowStatus v1alpha1.WorkflowPhase) constants.AnalysisStatus {
	if workflowStatus == v1alpha1.WorkflowSucceeded {
		return constants.Completed
	}
	return constants.Failed
}

func main() {
	var (
		err           error
		cleanUpURL    = flag.String("cleanup-url", "http://webhook-eventsource-svc.argo-events/batch/cleanup", "The URL to POST to to clean up a workflow")
		namespace     = flag.String("namespace", "argo", "The namespace to use when looking up workflows")
		appExposerURL = flag.String("app-exposer-url", "http://app-exposer", "The URL to use when connecting to app-exposer")
		setStatusURL  = flag.String("set-status-url", "http://webhook-eventsource-svc.argo-events/batch", "The URL to use when updating the status")
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
		log.Printf("%s\t%s\n", wf.Status.Phase, wf.Name)

		// If the workflow is completed, make sure the status in the DE matches.
		if wf.Status.Phase.Completed() {
			log.Printf("\t%s is Completed according to Argo\n", wf.Name)

			externalID := wf.ObjectMeta.Labels[constants.LabelKeyExternalID]
			log.Printf("\t%s has an external ID of %s\n", wf.Name, externalID)

			deStatus, err := analysisStatus(*appExposerURL, externalID)
			if err != nil {
				log.Printf("\t%s\n", err)
				continue
			}
			log.Printf("\t%s has a DE status of %s\n", wf.Name, deStatus)

			wfToDEStatus := workflowStatusToDEStatus(wf.Status.Phase)

			if deStatus != wfToDEStatus {
				log.Printf("\t%s has a DE status of %s and it should be %s. Fixing...\n", wf.Name, deStatus, wfToDEStatus)
				if err = sendStatus(*setStatusURL, externalID, "setting status from workflow-cleanup", wfToDEStatus); err != nil {
					log.Printf("\t%s\n", err)
					continue
				}
				log.Printf("\tDone fixing the DE status for %s to %s\n", wf.Name, wfToDEStatus)
			} else {
				log.Printf("\t%s has matching statuses in the DE and Argo, sending clean up request...\n", wf.Name)
				if err = sendCleanupMessage(*cleanUpURL, externalID); err != nil {
					log.Printf("\t%s\n", err)
					continue
				}
				log.Printf("\tSuccessfully sent a clean up request for %s\n", wf.Name)
			}
		}
	}
}
