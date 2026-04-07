package operator

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/labstack/echo/v4"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatusResponse describes the state of an analysis's K8s resources.
type StatusResponse struct {
	AnalysisID  string           `json:"analysisID"`
	Deployments []DeploymentInfo `json:"deployments"`
	Pods        []PodInfo        `json:"pods"`
	Services    []string         `json:"services"`
	Routes      []string         `json:"routes,omitempty"`
}

// DeploymentInfo holds basic deployment status.
type DeploymentInfo struct {
	Name            string `json:"name"`
	ReadyReplicas   int32  `json:"readyReplicas"`
	DesiredReplicas int32  `json:"desiredReplicas"`
}

// PodInfo holds basic pod status.
type PodInfo struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Ready bool   `json:"ready"`
}

// HandleStatus returns the status of all K8s resources for an analysis.
//
//	@Summary		Get analysis status
//	@Description	Returns the status of all K8s resources (deployments, pods,
//	@Description	services, routes) for the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	StatusResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/status [get]
func (o *Operator) HandleStatus(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}
	log.Debugf("status check for analysis %s", analysisID)

	opts := analysisLabelSelector(analysisID)
	resp := StatusResponse{AnalysisID: analysisID}

	// Deployments
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, d := range deps.Items {
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		resp.Deployments = append(resp.Deployments, DeploymentInfo{
			Name:            d.Name,
			ReadyReplicas:   d.Status.ReadyReplicas,
			DesiredReplicas: desired,
		})
	}

	// Pods
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, p := range pods.Items {
		resp.Pods = append(resp.Pods, PodInfo{
			Name:  p.Name,
			Phase: string(p.Status.Phase),
			Ready: isPodReady(p),
		})
	}

	// Services
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, s := range svcs.Items {
		resp.Services = append(resp.Services, s.Name)
	}

	// HTTPRoutes
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, r := range routes.Items {
		resp.Routes = append(resp.Routes, r.Name)
	}

	return c.JSON(http.StatusOK, resp)
}

// HandleURLReady checks if deployment has ready replicas, service exists,
// and an HTTPRoute exists for the given analysis.
//
//	@Summary		Check if analysis URL is ready
//	@Description	Returns whether the analysis has ready replicas, a service,
//	@Description	and an HTTPRoute.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	operatorclient.URLReadyResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/url-ready [get]
func (o *Operator) HandleURLReady(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}
	log.Debugf("url-ready check for analysis %s", analysisID)

	opts := analysisLabelSelector(analysisID)

	// Check deployment ready replicas.
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !hasReadyDeployment(deps.Items) {
		return c.JSON(http.StatusOK, operatorclient.URLReadyResponse{Ready: false})
	}

	// Check service existence.
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if len(svcs.Items) == 0 {
		return c.JSON(http.StatusOK, operatorclient.URLReadyResponse{Ready: false})
	}

	// Check HTTPRoute existence.
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if len(routes.Items) == 0 {
		return c.JSON(http.StatusOK, operatorclient.URLReadyResponse{Ready: false})
	}

	// Attempt to get the access URL from the vice-proxy sidecar.
	// Use the first service found for this analysis.
	accessURL, err := o.getAccessURL(ctx, svcs.Items[0].Name)
	if err != nil {
		log.Debugf("analysis %s: vice-proxy not yet reachable: %v", analysisID, err)
		return c.JSON(http.StatusOK, operatorclient.URLReadyResponse{Ready: false})
	}

	return c.JSON(http.StatusOK, operatorclient.URLReadyResponse{
		Ready:     true,
		AccessURL: accessURL,
	})
}

// HandlePods returns pod information for an analysis.
//
//	@Summary		Get analysis pods
//	@Description	Returns pod name, phase, and readiness for all pods
//	@Description	belonging to the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{array}		PodInfo
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/pods [get]
func (o *Operator) HandlePods(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}
	log.Debugf("pods check for analysis %s", analysisID)

	opts := analysisLabelSelector(analysisID)
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	var podInfos []PodInfo
	for _, p := range pods.Items {
		podInfos = append(podInfos, PodInfo{
			Name:  p.Name,
			Phase: string(p.Status.Phase),
			Ready: isPodReady(p),
		})
	}

	return c.JSON(http.StatusOK, podInfos)
}

// hasReadyDeployment returns true if any deployment in the list has at least
// one ready replica.
func hasReadyDeployment(deps []appsv1.Deployment) bool {
	return slices.ContainsFunc(deps, func(d appsv1.Deployment) bool {
		return d.Status.ReadyReplicas > 0
	})
}

// isPodReady returns true if the pod has a PodReady condition set to True.
func isPodReady(p apiv1.Pod) bool {
	return slices.ContainsFunc(p.Status.Conditions, func(cond apiv1.PodCondition) bool {
		return cond.Type == apiv1.PodReady && cond.Status == apiv1.ConditionTrue
	})
}

// HandleLogs returns container logs for an analysis's pods.
//
//	@Summary		Get analysis logs
//	@Description	Returns container logs for pods belonging to the given analysis.
//	@Description	Supports filtering by container, tail lines, and time.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Param			container	query		string	false	"The container name (default: analysis)"
//	@Param			tail-lines	query		int		false	"Number of lines from the end"
//	@Param			since		query		int		false	"Seconds in the past"
//	@Param			since-time	query		int		false	"Epoch timestamp"
//	@Param			previous	query		bool	false	"Previously terminated container"
//	@Param			timestamps	query		bool	false	"Include timestamps"
//	@Success		200			{object}	reporting.VICELogEntry
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/logs [get]
func (o *Operator) HandleLogs(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}
	log.Debugf("logs request for analysis %s", analysisID)

	logOpts := &apiv1.PodLogOptions{
		Follow: false,
	}

	// container is optional, but should have a default value of "analysis"
	if container := c.QueryParam("container"); container != "" {
		logOpts.Container = container
	} else {
		logOpts.Container = "analysis"
	}

	if prevStr := c.QueryParam("previous"); prevStr != "" {
		if previous, err := strconv.ParseBool(prevStr); err == nil {
			logOpts.Previous = previous
		}
	}

	if sinceStr := c.QueryParam("since"); sinceStr != "" {
		if since, err := strconv.ParseInt(sinceStr, 10, 64); err == nil {
			logOpts.SinceSeconds = &since
		}
	}

	if sinceTimeStr := c.QueryParam("since-time"); sinceTimeStr != "" {
		if sinceTime, err := strconv.ParseInt(sinceTimeStr, 10, 64); err == nil {
			t := metav1.Unix(sinceTime, 0)
			logOpts.SinceTime = &t
		}
	}

	if tailStr := c.QueryParam("tail-lines"); tailStr != "" {
		if tailLines, err := strconv.ParseInt(tailStr, 10, 64); err == nil {
			logOpts.TailLines = &tailLines
		}
	}

	if tsStr := c.QueryParam("timestamps"); tsStr != "" {
		if timestamps, err := strconv.ParseBool(tsStr); err == nil {
			logOpts.Timestamps = timestamps
		}
	}

	opts := analysisLabelSelector(analysisID)
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if len(pods.Items) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "no pods found for analysis")
	}

	// Match app-exposer's original LogsHandler behavior by returning logs for
	// only the first pod. VICE analyses are single-replica deployments, so
	// there is typically only one pod, and the UI expects a single log entry
	// (VICELogEntry) rather than an array of logs for all pods/containers.
	pod := pods.Items[0]

	logReq := o.clientset.CoreV1().Pods(o.namespace).GetLogs(pod.Name, logOpts)
	stream, err := logReq.Stream(ctx)
	if err != nil {
		log.Errorf("error streaming logs for %s/%s: %v", pod.Name, logOpts.Container, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	defer func() { _ = stream.Close() }()

	logBytes, err := io.ReadAll(stream)
	if err != nil {
		log.Errorf("error reading logs for %s/%s: %v", pod.Name, logOpts.Container, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	bodyLines := strings.Split(string(logBytes), "\n")
	newSinceTime := fmt.Sprintf("%d", time.Now().Unix())

	// Return the same format as the original app-exposer LogsHandler.
	return c.JSON(http.StatusOK, &reporting.VICELogEntry{
		SinceTime: newSinceTime,
		Lines:     bodyLines,
	})
}
