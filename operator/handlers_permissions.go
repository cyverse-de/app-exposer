package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/labstack/echo/v4"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PermissionsResponse is the response body for HandleGetPermissions.
type PermissionsResponse struct {
	AllowedUsers []string `json:"allowedUsers"`
}

// UpdatePermissionsRequest is the request body for HandleUpdatePermissions.
type UpdatePermissionsRequest struct {
	AllowedUsers []string `json:"allowedUsers"`
}

// findPermissionsConfigMap locates the permissions ConfigMap for an analysis
// by its analysis-id label and permissions- name prefix.
func (o *Operator) findPermissionsConfigMap(ctx context.Context, analysisID string) (*apiv1.ConfigMap, error) {
	opts := analysisLabelSelector(analysisID)
	cmList, err := o.clientset.CoreV1().ConfigMaps(o.namespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}

	prefix := constants.PermissionsConfigMapPrefix + "-"
	for i := range cmList.Items {
		if strings.HasPrefix(cmList.Items[i].Name, prefix) {
			return &cmList.Items[i], nil
		}
	}
	return nil, nil
}

// HandleGetPermissions returns the list of users allowed to access an analysis.
//
//	@Summary		Get analysis permissions
//	@Description	Returns the allowed-users list from the permissions ConfigMap
//	@Description	for the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	PermissionsResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/permissions [get]
func (o *Operator) HandleGetPermissions(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}

	permsCM, err := o.findPermissionsConfigMap(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if permsCM == nil {
		return echo.NewHTTPError(http.StatusNotFound, "permissions configmap not found for analysis "+analysisID)
	}

	// Parse the allowed-users file: one username per line, skip blanks.
	var users []string
	for line := range strings.SplitSeq(permsCM.Data[constants.PermissionsFileName], "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			users = append(users, line)
		}
	}

	return c.JSON(http.StatusOK, PermissionsResponse{AllowedUsers: users})
}

// HandleUpdatePermissions rewrites the permissions ConfigMap for an analysis
// with a new list of allowed users.
//
//	@Summary		Update analysis permissions
//	@Description	Replaces the allowed-users list in the permissions ConfigMap
//	@Description	for the given analysis. The full list must be provided (not incremental).
//	@Tags			analyses
//	@Accept			json
//	@Param			analysis-id	path	string						true	"The analysis ID"
//	@Param			request		body	UpdatePermissionsRequest	true	"The new allowed users list"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		404	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/permissions [put]
func (o *Operator) HandleUpdatePermissions(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}

	var req UpdatePermissionsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Guard against accidentally clearing all access to the analysis.
	if len(req.AllowedUsers) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "allowedUsers must not be empty")
	}

	log.Infof("updating permissions for analysis %s (%d users)", analysisID, len(req.AllowedUsers))

	permsCM, err := o.findPermissionsConfigMap(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if permsCM == nil {
		return echo.NewHTTPError(http.StatusNotFound, "permissions configmap not found for analysis "+analysisID)
	}

	// Build the new allowed-users content (one username per line, trailing newline).
	if permsCM.Data == nil {
		permsCM.Data = make(map[string]string)
	}
	permsCM.Data[constants.PermissionsFileName] = strings.Join(req.AllowedUsers, "\n") + "\n"

	if _, err := o.clientset.CoreV1().ConfigMaps(o.namespace).Update(ctx, permsCM, metav1.UpdateOptions{}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	log.Infof("permissions updated for analysis %s", analysisID)
	return c.NoContent(http.StatusOK)
}

// viceProxyURL builds the in-cluster URL for a vice-proxy sidecar endpoint.
func (o *Operator) viceProxyURL(svcName, path string) string {
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s:%d", svcName, o.namespace, constants.VICEProxyServicePort),
		Path:   path,
	}
	return u.String()
}

// forwardToViceProxy finds the analysis service, builds a vice-proxy URL,
// makes the HTTP request, and returns the response body. Returns an echo
// HTTPError on failure so handlers can return it directly.
func (o *Operator) forwardToViceProxy(ctx context.Context, analysisID, method, path string, reqBody io.Reader) ([]byte, error) {
	svc, err := o.findAnalysisService(ctx, analysisID)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if svc == nil {
		return nil, echo.NewHTTPError(http.StatusNotFound, "no service found for analysis "+analysisID)
	}

	proxyURL := o.viceProxyURL(svc.Name, path)
	log.Infof("forwarding %s %s for analysis %s", method, path, analysisID)

	req, err := http.NewRequestWithContext(ctx, method, proxyURL, reqBody)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("creating proxy request: %v", err))
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := noRedirectHTTPClient.Do(req)
	if err != nil {
		log.Errorf("%s request to vice-proxy failed for analysis %s: %v", path, analysisID, err)
		return nil, echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("failed to reach vice-proxy: %v", err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("reading vice-proxy response: %v", err))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Errorf("vice-proxy returned %d for %s on analysis %s: %s", resp.StatusCode, path, analysisID, string(body))
		return nil, echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("vice-proxy returned %d", resp.StatusCode))
	}

	return body, nil
}

// findAnalysisService returns the first Service matching the analysis-id label,
// or nil if none exists.
func (o *Operator) findAnalysisService(ctx context.Context, analysisID string) (*apiv1.Service, error) {
	opts := analysisLabelSelector(analysisID)
	svcList, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing services for analysis %s: %w", analysisID, err)
	}
	if len(svcList.Items) == 0 {
		return nil, nil
	}
	return &svcList.Items[0], nil
}

// HandleGetActiveSessions returns the list of active user sessions for an
// analysis by forwarding the request to the vice-proxy sidecar.
//
//	@Summary		List active sessions for an analysis
//	@Description	Returns the list of currently authenticated user sessions from
//	@Description	the vice-proxy sidecar. Each entry includes the session ID and
//	@Description	username. Use this to see who is logged in before calling
//	@Description	POST /logout-user to remove a specific user.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	operatorclient.ActiveSessionsResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		502			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/active-sessions [get]
func (o *Operator) HandleGetActiveSessions(c echo.Context) error {
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}

	body, err := o.forwardToViceProxy(c.Request().Context(), analysisID, http.MethodGet, "/active-sessions", nil)
	if err != nil {
		return err
	}
	return c.JSONBlob(http.StatusOK, body)
}

// HandleLogoutUser invalidates all sessions for a specific user in an analysis
// by forwarding the request to the vice-proxy sidecar.
//
//	@Summary		Admin logout (by username, no cookie needed)
//	@Description	Invalidates all active sessions for the given username in the
//	@Description	vice-proxy sidecar. Use this to kick a specific user out of an
//	@Description	analysis without needing their browser cookie or a Keycloak
//	@Description	logout token. Does not trigger Keycloak SSO logout — the user
//	@Description	remains logged in to other applications in the realm. Use
//	@Description	GET /active-sessions first to see who is currently logged in.
//	@Tags			analyses
//	@Accept			json
//	@Produce		json
//	@Param			analysis-id	path		string								true	"The analysis ID"
//	@Param			request		body		operatorclient.LogoutUserRequest	true	"The user to log out"
//	@Success		200			{object}	operatorclient.LogoutUserResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		502			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/logout-user [post]
func (o *Operator) HandleLogoutUser(c echo.Context) error {
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}

	var req operatorclient.LogoutUserRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.Username == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username is required")
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("marshalling request: %v", err))
	}

	body, err := o.forwardToViceProxy(c.Request().Context(), analysisID, http.MethodPost, "/logout-user", bytes.NewReader(reqBody))
	if err != nil {
		return err
	}

	log.Infof("logout-user forwarded for analysis %s (user %s)", analysisID, req.Username)
	return c.JSONBlob(http.StatusOK, body)
}
