package httphandlers

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/labstack/echo/v4"
)

type FilteredDeploymentsResponse struct {
	Deployments []incluster.DeploymentInfo `json:"deployments"`
}

// @ID				filterable-deployments
// @Summary		Lists all of the deployments.
// @Description	Returns a filtered listing of deployments in use by VICE apps.
// @Description	The key-value pairs in the query string are used to filter the deployments.
// @Description	The key-value pairs are not listed as parameters.
// @Produce		json
// @Success		200	{object}	FilteredDeploymentsResponse
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/listing/deployments [get]
func (h *HTTPHandlers) FilterableDeploymentsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	deployments, err := h.incluster.GetFilteredDeployments(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, FilteredDeploymentsResponse{
		Deployments: deployments,
	})
}

type FilteredPodsResponse struct {
	Pods []incluster.PodInfo `json:"pods"`
}

// @ID				filterable-pods
// @Summary		Returns a listing of the pods in a VICE analysis.
// @Description	Returns a filtered listing of pods in use by VICE apps.
// @Description	The key-value pairs in the query string are used to filter the pods.
// @Description	The key-value pairs are not listed as parameters.
// @Produce		json
// @Success		200	{object}	FilteredPodsResponse
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/listing/pods [get]
func (h *HTTPHandlers) FilterablePodsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	pods, err := h.incluster.GetFilteredPods(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, FilteredPodsResponse{
		Pods: pods,
	})
}

type FilteredConfigMapsResponse struct {
	ConfigMaps []incluster.ConfigMapInfo `json:"configmaps"`
}

// @ID				filterable-configmaps
// @Summary		Lists configmaps in use by VICE apps.
// @Description	Lists configmaps in use by VICE apps. The query parameters
// @Description	are used to filter the results and aren't listed as parameters.
// @Produce		json
// @Success		200	{object}	FilteredConfigMapsResponse
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/listing/configmaps [get]
func (h *HTTPHandlers) FilterableConfigMapsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	cms, err := h.incluster.GetFilteredConfigMaps(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, FilteredConfigMapsResponse{
		ConfigMaps: cms,
	})
}

type FilteredServicesResponse struct {
	Services []incluster.ServiceInfo `json:"services"`
}

// @ID				filterable-services
// @Summary		Lists services in use by VICE apps.
// @Description	Lists services in use by VICE apps. The query parameters
// @Description	are used to filter the results and aren't listed as parameters.
// @Produce		json
// @Success		200	{object}	FilteredServicesResponse
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/listing/services [get]
func (h *HTTPHandlers) FilterableServicesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	svcs, err := h.incluster.GetFilteredServices(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, FilteredServicesResponse{
		Services: svcs,
	})
}

type FilteredRoutesResponse struct {
	Routes []incluster.RouteInfo `json:"routes"`
}

// @ID				filterable-routes
// @Summary		Lists HTTP routes in use by VICE apps.
// @Description	Lists HTTP routes in use by VICE apps. The query parameters
// @Description	are used to filter the results and aren't listed as parameters.
// @Produce		json
// @Success		200	{object}	FilteredRoutesResponse
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/listing/routes [get]
func (h *HTTPHandlers) FilterableRoutesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	routes, err := h.incluster.GetFilteredRoutes(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, FilteredRoutesResponse{
		Routes: routes,
	})
}

// @ID				admin-describe-analysis
// @Summary		Lists resources by subdomain
// @Description	Returns a listing entry for a single analysis
// @Description	associated with the host/subdomain passed in as 'host' from the URL.
// @Param			host	path		string	true	"Host/Subdomain"
// @Success		200		{object}	incluster.ResourceInfo
// @Failure		400		{object}	common.ErrorResponse
// @Failure		500		{object}	common.ErrorResponse
// @Router			/vice/admin/{host}/description [get]
func (h *HTTPHandlers) AdminDescribeAnalysisHandler(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Param("host")

	filter := map[string]string{
		"subdomain": host,
	}

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, listing)
}

// @ID				describe-analysis
// @Summary		Returns resources by user and subdomain.
// @Description	Returns a listing entry for a single analysis associated
// @Description	with the host/subdomain passed in as 'host' from the URL.
// @Description	The user passed in must have access to the VICE analysis.
// @Produce		json
// @Param			user	query		string	true	"username"
// @Param			host	path		string	tru		"subdomain"
// @Success		200		{object}	incluster.ResourceInfo
// @Failure		400		{object}	common.ErrorResponse
// @Failure		403		{object}	common.ErrorResponse
// @Failure		404		{object}	common.ErrorResponse
// @Failure		500		{object}	common.ErrorResponse
// @Router			/vice/{host}/description [get]
func (h *HTTPHandlers) DescribeAnalysisHandler(c echo.Context) error {
	ctx := c.Request().Context()

	log.Info("in DescribeAnalysisHandler")
	host := c.Param("host")
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	log.Infof("user: %s, user suffix: %s, host: %s", user, h.incluster.UserSuffix, host)

	// Since some usernames don't come through the labelling process unscathed, we have to use
	// the user ID.
	fixedUser := h.incluster.FixUsername(user)
	_, err := h.apps.GetUserID(ctx, fixedUser)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("user %s not found", fixedUser))
		}
		return err
	}

	log.Infof("2 user: %s, user suffix: %s, host: %s", user, h.incluster.UserSuffix, host)

	filter := map[string]string{
		"subdomain": host,
	}

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return err
	}

	// the permissions checks occur after the listing because it's possible for the listing to happen
	// before the subdomain is set in the database, causing an error to get percolated up to the UI.
	// Waiting until the Deployments list contains at least one item should guarantee that the subdomain
	// is set in the database.
	if len(listing.Deployments) > 0 {
		externalID := listing.Deployments[0].ExternalID
		analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
		if err != nil {
			return err
		}

		// Make sure the user has permissions to look up info about this analysis.
		p := &permissions.Permissions{
			BaseURL: h.incluster.PermissionsURL,
		}

		allowed, err := p.IsAllowed(ctx, user, analysisID)
		if err != nil {
			return err
		}

		if !allowed {
			return echo.NewHTTPError(http.StatusForbidden, fmt.Sprintf("user %s cannot access analysis %s", user, analysisID))
		}
	}

	return c.JSON(http.StatusOK, listing)
}

// @ID				filterable-resources
// @Summary		Returns resources for a VICE analysis
// @Description	Returns all of the k8s resources associated with a VICE analysis
// @Description	but checks permissions to see if the requesting user has permission
// @Description	to access the resource. The rest of the query map is used to filter
// @Description	resources returned from the handler.
// @Produce		json
// @Param			user	query		string	true	"username"
// @Success		200		{object}	incluster.ResourceInfo
// @Failure		400		{object}	common.ErrorResponse
// @Failure		403		{object}	common.ErrorResponse
// @Failure		404		{object}	common.ErrorResponse
// @Failure		500		{object}	common.ErrorResponse
// @Router			/vice/listing [get]
func (h *HTTPHandlers) FilterableResourcesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	// Since some usernames don't come through the labelling process unscathed, we have to use
	// the user ID.
	user = h.incluster.FixUsername(user)
	userID, err := h.apps.GetUserID(ctx, user)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("user %s not found", user))
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	filter := common.FilterMap(c.Request().URL.Query())
	delete(filter, "user")

	filter["user-id"] = userID

	log.Debugf("user ID is %s", userID)

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, listing)
}

// @ID				admin-filterable-resources
// @Summary		Lists resources based on a filter
// @Description	Returns k8s resources in the cluster based on the filter. The query
// @Description	parameters are used as the filter and are not listed as params here.
// @Produce		json
// @Success		200	{object}	incluster.ResourceInfo
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/admin/listing [get]
func (h *HTTPHandlers) AdminFilterableResourcesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, listing)
}

type ListPodsResponse struct {
	Pods []incluster.RetPod `json:"pods"`
}

// @ID				list-pods
// @Summary		Lists the k8s pods associated with the provided external-id
// @Description	Lists the k8s pods associated with the provided external-id. For now
// @Description	just returns pod info in the format `{"pods" : [{}]}`
// @Produce		json
// @Param			analysis-id	path		string	true	"Analysis ID"
// @Param			user		query		string	true	"Username"
// @Success		200			{object}	ListPodsResponse
// @Failure		400			{object}	common.ErrorResponse
// @Failure		403			{object}	common.ErrorResponse
// @Failure		404			{object}	common.ErrorResponse
// @Failure		500			{object}	common.ErrorResponse
// @Router			/vice/{analysis-id}/pods [get]
func (h *HTTPHandlers) PodsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	analysisID := c.Param("analysis-id")
	user := c.QueryParam("user")

	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user not set")
	}

	externalIDs, err := h.incluster.GetExternalIDs(ctx, user, analysisID)
	if err != nil {
		return err
	}

	if len(externalIDs) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("no external-id found for analysis-id %s", analysisID))
	}

	// For now, just use the first external ID
	externalID := externalIDs[0]

	returnedPods, err := h.incluster.GetPods(ctx, externalID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, ListPodsResponse{
		Pods: returnedPods,
	})
}
