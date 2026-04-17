package incluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// VICEStep contains information about an analysis step associated with a running
// VICE job.
type VICEStep struct {
	Name          string               `json:"name"`
	ExternalID    constants.ExternalID `json:"external_id"`
	StartDate     string               `json:"startdate"`
	EndDate       string               `json:"enddate"`
	Status        string               `json:"status"`
	AppStepNumber int                  `json:"app_step_number"`
	StepType      string               `json:"step_type"`
}

// VICEAnalysis contains information about an analysis associated with a running
// VICE job.
type VICEAnalysis struct {
	AnalysisID constants.AnalysisID `json:"analysis_id"`
	Steps      []VICEStep           `json:"steps"`
	Timestamp  string               `json:"timestamp"`
	Total      int                  `json:"total"`
}

func (i *Incluster) GetExternalIDs(ctx context.Context, user string, analysisID constants.AnalysisID) ([]constants.ExternalID, error) {
	var (
		err               error
		analysisLookupURL *url.URL
	)

	analysisLookupURL, err = url.Parse(i.AppsServiceBaseURL)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing url %s", i.AppsServiceBaseURL)
	}
	analysisLookupURL.Path = path.Join("/analyses", string(analysisID), "steps")
	q := analysisLookupURL.Query()
	q.Set("user", user)
	analysisLookupURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, analysisLookupURL.String(), nil)
	if err != nil {
		return nil, errors.Wrapf(err, "error from GET %s", analysisLookupURL.String())
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "error from GET %s", analysisLookupURL.String())
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "error reading response body from %s", analysisLookupURL.String())
	}

	parsedResponse := &VICEAnalysis{
		Steps: []VICEStep{},
	}

	if err = json.Unmarshal(body, parsedResponse); err != nil {
		return nil, errors.Wrapf(err, "error unmarshalling JSON from %s", analysisLookupURL.String())
	}

	retval := []constants.ExternalID{}

	for _, step := range parsedResponse.Steps {
		retval = append(retval, step.ExternalID)
	}

	return retval, nil
}

// Contains information about pods returned by the VICEPods handler.
type RetPod struct {
	Name string `json:"name"`
}

func (i *Incluster) GetPods(ctx context.Context, externalID constants.ExternalID) ([]RetPod, error) {
	set := labels.Set(map[string]string{
		constants.ExternalIDLabel: string(externalID),
	})

	listoptions := metav1.ListOptions{
		LabelSelector: set.AsSelector().String(),
	}

	returnedPods := []RetPod{}

	podlist, err := i.clientset.CoreV1().Pods(i.ViceNamespace).List(ctx, listoptions)
	if err != nil {
		return nil, err
	}

	for _, p := range podlist.Items {
		returnedPods = append(returnedPods, RetPod{Name: p.Name})
	}

	return returnedPods, nil
}
