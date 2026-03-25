package operator

import (
	"fmt"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
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

// URLReadyResponse indicates whether a VICE analysis is ready for user access.
type URLReadyResponse struct {
	Ready bool `json:"ready"`
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
//	@Success		200			{object}	URLReadyResponse
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
	podReady := hasReadyDeployment(deps.Items)

	// Check service exists.
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	serviceExists := len(svcs.Items) > 0

	// Check HTTPRoute exists.
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	routingExists := len(routes.Items) > 0

	return c.JSON(http.StatusOK, URLReadyResponse{
		Ready: podReady && serviceExists && routingExists,
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
	for _, d := range deps {
		if d.Status.ReadyReplicas > 0 {
			return true
		}
	}
	return false
}

// isPodReady returns true if the pod has a PodReady condition set to True.
func isPodReady(p apiv1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == apiv1.PodReady && cond.Status == apiv1.ConditionTrue {
			return true
		}
	}
	return false
}

// LogEntry holds a single container's log output. If log retrieval failed,
// Error is set and Log is empty.
type LogEntry struct {
	PodName       string `json:"podName"`
	ContainerName string `json:"containerName"`
	Log           string `json:"log"`
	Error         string `json:"error,omitempty"`
}

// HandleLogs returns container logs for an analysis's pods.
//
//	@Summary		Get analysis logs
//	@Description	Returns the last 5 minutes of container logs for all pods
//	@Description	belonging to the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{array}		LogEntry
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

	opts := analysisLabelSelector(analysisID)
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	sinceSeconds := int64(300)
	logOpts := &apiv1.PodLogOptions{
		SinceSeconds: &sinceSeconds,
	}

	var entries []LogEntry
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			logOpts.Container = container.Name
			logReq := o.clientset.CoreV1().Pods(o.namespace).GetLogs(pod.Name, logOpts)
			stream, err := logReq.Stream(ctx)
			if err != nil {
				log.Errorf("error getting logs for %s/%s: %v", pod.Name, container.Name, err)
				entries = append(entries, LogEntry{
					PodName: pod.Name, ContainerName: container.Name,
					Error: fmt.Sprintf("failed to stream logs: %v", err),
				})
				continue
			}
			logBytes, err := io.ReadAll(stream)
			_ = stream.Close() //nolint:errcheck // best-effort close inside loop; any error is secondary to read error above
			if err != nil {
				log.Errorf("error reading logs for %s/%s: %v", pod.Name, container.Name, err)
				entries = append(entries, LogEntry{
					PodName: pod.Name, ContainerName: container.Name,
					Error: fmt.Sprintf("failed to read logs: %v", err),
				})
				continue
			}
			entries = append(entries, LogEntry{
				PodName:       pod.Name,
				ContainerName: container.Name,
				Log:           string(logBytes),
			})
		}
	}

	return c.JSON(http.StatusOK, entries)
}
