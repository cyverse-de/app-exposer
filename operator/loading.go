package operator

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

//go:embed templates/loading.html
var loadingTemplateFS embed.FS

// loadingTemplate is the parsed loading page template. Parsed once at init
// time rather than on every request.
var loadingTemplate = template.Must(template.ParseFS(loadingTemplateFS, "templates/loading.html"))

// Stage constants for the loading page status response.
const (
	StageDeploying   = "deploying"
	StageStarting    = "starting"
	StageAlmostReady = "almost-ready"
	StageReady       = "ready"
	StageError       = "error"
)

// LoadingStatusResponse is the JSON response for the loading page status endpoint.
type LoadingStatusResponse struct {
	Ready bool             `json:"ready"`
	Stage string           `json:"stage"`
	Error string           `json:"error"`
	Pods  []LoadingPodInfo `json:"pods"`
}

// LoadingPodInfo holds pod status for the loading page.
type LoadingPodInfo struct {
	Name              string                   `json:"name"`
	Phase             string                   `json:"phase"`
	Ready             bool                     `json:"ready"`
	RestartCount      int32                    `json:"restartCount"`
	ContainerStatuses []LoadingContainerStatus `json:"containerStatuses"`
}

// LoadingContainerStatus holds per-container status for the loading page.
type LoadingContainerStatus struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Reason       string `json:"reason"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restartCount"`
}

// loadingPageData is the template data for the loading page.
type loadingPageData struct {
	AppName    string
	AnalysisID string
	TimeoutMs  int64
}

// computeStage determines the loading stage from pod state and resource readiness.
// Returns the stage string and an error message (empty if no error).
func computeStage(pods []apiv1.Pod, depReady, svcExists bool) (string, string) {
	if len(pods) == 0 {
		return StageDeploying, ""
	}

	// Check for error conditions first.
	for _, pod := range pods {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.RestartCount > 2 && cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				if reason == "CrashLoopBackOff" || reason == "Error" {
					return StageError, fmt.Sprintf("container %q is in %s (restarted %d times)",
						cs.Name, reason, cs.RestartCount)
				}
			}
		}
	}

	// Check if all pods are pending.
	allPending := true
	for _, pod := range pods {
		if pod.Status.Phase != apiv1.PodPending {
			allPending = false
			break
		}
	}
	if allPending {
		return StageDeploying, ""
	}

	// Check if deployment is ready and service exists.
	if depReady && svcExists {
		return StageReady, ""
	}

	// Check if all pod containers are ready.
	allContainersReady := true
	for _, pod := range pods {
		if !isPodReady(pod) {
			allContainersReady = false
			break
		}
	}
	if allContainersReady {
		return StageAlmostReady, ""
	}

	return StageStarting, ""
}

// buildLoadingPodInfo converts K8s Pod objects to LoadingPodInfo.
func buildLoadingPodInfo(pods []apiv1.Pod) []LoadingPodInfo {
	result := make([]LoadingPodInfo, 0, len(pods))
	for _, pod := range pods {
		var totalRestarts int32
		var containers []LoadingContainerStatus

		for _, cs := range pod.Status.ContainerStatuses {
			totalRestarts += cs.RestartCount
			containers = append(containers, containerStatusToLoading(cs))
		}
		for _, cs := range pod.Status.InitContainerStatuses {
			totalRestarts += cs.RestartCount
			containers = append(containers, containerStatusToLoading(cs))
		}

		result = append(result, LoadingPodInfo{
			Name:              pod.Name,
			Phase:             string(pod.Status.Phase),
			Ready:             isPodReady(pod),
			RestartCount:      totalRestarts,
			ContainerStatuses: containers,
		})
	}
	return result
}

// containerStatusToLoading converts a K8s ContainerStatus to LoadingContainerStatus.
func containerStatusToLoading(cs apiv1.ContainerStatus) LoadingContainerStatus {
	state, reason := containerStateString(cs.State)
	return LoadingContainerStatus{
		Name:         cs.Name,
		State:        state,
		Reason:       reason,
		Ready:        cs.Ready,
		RestartCount: cs.RestartCount,
	}
}

// containerStateString returns a human-readable state and reason from a ContainerState.
func containerStateString(state apiv1.ContainerState) (string, string) {
	if state.Running != nil {
		return "running", ""
	}
	if state.Waiting != nil {
		return "waiting", state.Waiting.Reason
	}
	if state.Terminated != nil {
		return "terminated", state.Terminated.Reason
	}
	return "unknown", ""
}

// HandleLoadingPage serves the loading page HTML for the analysis identified
// by the request's Host header subdomain.
func (o *Operator) HandleLoadingPage(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Request().Host

	analysisID, appName, err := o.resolveSubdomain(ctx, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Analysis not found.")
	}

	data := loadingPageData{
		AppName:    appName,
		AnalysisID: analysisID,
		TimeoutMs:  o.loadingTimeoutMs,
	}

	c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
	c.Response().WriteHeader(http.StatusOK)
	return loadingTemplate.Execute(c.Response().Writer, data)
}

// HandleLoadingStatus returns the current loading status for the analysis
// identified by the request's Host header subdomain. If the analysis is ready,
// performs the route swap before responding.
func (o *Operator) HandleLoadingStatus(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Request().Host

	analysisID, _, err := o.resolveSubdomain(ctx, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Analysis not found.")
	}

	opts := analysisLabelSelector(analysisID)

	// Check deployment readiness.
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	depReady := false
	for _, d := range deps.Items {
		if d.Status.ReadyReplicas > 0 {
			depReady = true
			break
		}
	}

	// Check service existence.
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	svcExists := len(svcs.Items) > 0

	// Get pods.
	podList, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	stage, errMsg := computeStage(podList.Items, depReady, svcExists)
	ready := stage == StageReady

	// Perform route swap if ready.
	if ready {
		if swapErr := o.SwapRoute(ctx, analysisID); swapErr != nil {
			log.Errorf("route swap failed for analysis %s: %v", analysisID, swapErr)
			// Don't fail the status response; report ready but log the swap error.
		}
	}

	return c.JSON(http.StatusOK, LoadingStatusResponse{
		Ready: ready,
		Stage: stage,
		Error: errMsg,
		Pods:  buildLoadingPodInfo(podList.Items),
	})
}

// resolveSubdomain extracts the subdomain from the Host header and looks up
// the analysis-id and app-name by listing Deployments with matching subdomain label.
// Returns analysisID, appName, error.
func (o *Operator) resolveSubdomain(ctx context.Context, host string) (string, string, error) {
	// Extract subdomain: take the first part before any dot or colon.
	subdomain := host
	if idx := strings.IndexByte(subdomain, '.'); idx != -1 {
		subdomain = subdomain[:idx]
	}
	if idx := strings.IndexByte(subdomain, ':'); idx != -1 {
		subdomain = subdomain[:idx]
	}

	if subdomain == "" {
		return "", "", fmt.Errorf("empty subdomain from host %q", host)
	}

	selector := labels.Set{"subdomain": subdomain}.AsSelector().String()
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return "", "", fmt.Errorf("listing deployments for subdomain %s: %w", subdomain, err)
	}
	if len(deps.Items) == 0 {
		return "", "", fmt.Errorf("no deployment found for subdomain %s", subdomain)
	}

	dep := deps.Items[0]
	analysisID := dep.Labels["analysis-id"]
	appName := dep.Labels["app-name"]

	return analysisID, appName, nil
}
