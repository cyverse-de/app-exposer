package httphandlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/labstack/echo/v4"
)

// FilteredDeploymentsResponse is the response body for the filterable deployments endpoint.
type FilteredDeploymentsResponse struct {
	Deployments []incluster.DeploymentInfo `json:"deployments"`
}

// FilterableDeploymentsHandler returns a filtered listing of VICE deployments.
// Query string key-value pairs are used as label filters.
//
//	@ID				filterable-deployments
//	@Summary		Lists all of the deployments.
//	@Description	Returns a filtered listing of deployments in use by VICE apps.
//	@Description	The key-value pairs in the query string are used to filter the deployments.
//	@Description	The key-value pairs are not listed as parameters.
//	@Produce		json
//	@Success		200	{object}	FilteredDeploymentsResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/listing/deployments [get]
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

// FilteredPodsResponse is the response body for the filterable pods endpoint.
type FilteredPodsResponse struct {
	Pods []incluster.PodInfo `json:"pods"`
}

// FilterablePodsHandler returns a filtered listing of VICE pods.
// Query string key-value pairs are used as label filters.
//
//	@ID				filterable-pods
//	@Summary		Returns a listing of the pods in a VICE analysis.
//	@Description	Returns a filtered listing of pods in use by VICE apps.
//	@Description	The key-value pairs in the query string are used to filter the pods.
//	@Description	The key-value pairs are not listed as parameters.
//	@Produce		json
//	@Success		200	{object}	FilteredPodsResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/listing/pods [get]
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

// FilteredConfigMapsResponse is the response body for the filterable configmaps endpoint.
type FilteredConfigMapsResponse struct {
	ConfigMaps []incluster.ConfigMapInfo `json:"configmaps"`
}

// FilterableConfigMapsHandler returns a filtered listing of VICE ConfigMaps.
// Query string key-value pairs are used as label filters.
//
//	@ID				filterable-configmaps
//	@Summary		Lists configmaps in use by VICE apps.
//	@Description	Lists configmaps in use by VICE apps. The query parameters
//	@Description	are used to filter the results and aren't listed as parameters.
//	@Produce		json
//	@Success		200	{object}	FilteredConfigMapsResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/listing/configmaps [get]
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

// FilteredServicesResponse is the response body for the filterable services endpoint.
type FilteredServicesResponse struct {
	Services []incluster.ServiceInfo `json:"services"`
}

// FilterableServicesHandler returns a filtered listing of VICE Services.
// Query string key-value pairs are used as label filters.
//
//	@ID				filterable-services
//	@Summary		Lists services in use by VICE apps.
//	@Description	Lists services in use by VICE apps. The query parameters
//	@Description	are used to filter the results and aren't listed as parameters.
//	@Produce		json
//	@Success		200	{object}	FilteredServicesResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/listing/services [get]
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

// FilteredRoutesResponse is the response body for the filterable routes endpoint.
type FilteredRoutesResponse struct {
	Routes []incluster.RouteInfo `json:"routes"`
}

// FilterableRoutesHandler returns a filtered listing of VICE HTTP routes.
// Query string key-value pairs are used as label filters.
//
//	@ID				filterable-routes
//	@Summary		Lists HTTP routes in use by VICE apps.
//	@Description	Lists HTTP routes in use by VICE apps. The query parameters
//	@Description	are used to filter the results and aren't listed as parameters.
//	@Produce		json
//	@Success		200	{object}	FilteredRoutesResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/listing/routes [get]
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

// AdminDescribeAnalysisHandler returns the K8s resource listing for the
// analysis whose subdomain matches the 'host' path parameter.
//
//	@ID				admin-describe-analysis
//	@Summary		Lists resources by subdomain
//	@Description	Returns a listing entry for a single analysis
//	@Description	associated with the host/subdomain passed in as 'host' from the URL.
//	@Param			host	path		string	true	"Host/Subdomain"
//	@Success		200		{object}	incluster.ResourceInfo
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/admin/{host}/description [get]
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

// DescribeAnalysisHandler returns the K8s resource listing for the analysis
// matching the given subdomain, checking that the requesting user has permission
// to access it.
//
//	@ID				describe-analysis
//	@Summary		Returns resources by user and subdomain.
//	@Description	Returns a listing entry for a single analysis associated
//	@Description	with the host/subdomain passed in as 'host' from the URL.
//	@Description	The user passed in must have access to the VICE analysis.
//	@Produce		json
//	@Param			user	query		string	true	"username"
//	@Param			host	path		string	true	"subdomain"
//	@Success		200		{object}	incluster.ResourceInfo
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		403		{object}	common.ErrorResponse
//	@Failure		404		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/{host}/description [get]
func (h *HTTPHandlers) DescribeAnalysisHandler(c echo.Context) error {
	ctx := c.Request().Context()

	host := c.Param("host")
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	// Since some usernames don't come through the labelling process unscathed, we have to use
	// the user ID.
	fixedUser := h.incluster.FixUsername(user)
	_, err := h.apps.GetUserID(ctx, fixedUser)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("user %s not found", fixedUser))
		}
		return err
	}

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

// FilterableResourcesHandler returns all K8s resources for the requesting user's
// VICE analyses, filtered by query string parameters. Requires a valid 'user'
// query parameter.
//
//	@ID				filterable-resources
//	@Summary		Returns resources for a VICE analysis
//	@Description	Returns all of the k8s resources associated with a VICE analysis
//	@Description	but checks permissions to see if the requesting user has permission
//	@Description	to access the resource. The rest of the query map is used to filter
//	@Description	resources returned from the handler.
//	@Produce		json
//	@Param			user	query		string	true	"username"
//	@Success		200		{object}	incluster.ResourceInfo
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		403		{object}	common.ErrorResponse
//	@Failure		404		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/listing [get]
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
		if errors.Is(err, sql.ErrNoRows) {
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

// AdminFilterableResourcesHandler returns K8s resources filtered by query string
// parameters without requiring user authentication.
//
//	@ID				admin-filterable-resources
//	@Summary		Lists resources based on a filter
//	@Description	Returns k8s resources in the cluster based on the filter. The query
//	@Description	parameters are used as the filter and are not listed as params here.
//	@Produce		json
//	@Success		200	{object}	incluster.ResourceInfo
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/listing [get]
func (h *HTTPHandlers) AdminFilterableResourcesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, listing)
}

// AdminOperatorListingHandler returns an aggregated listing of all running VICE
// analyses across all configured operators. Returns full resource info
// (deployments, pods, configmaps, services, routes) merged from all
// clusters. Partial results are returned if some operators are unreachable.
//
//	@ID				admin-operator-listing
//	@Summary		Lists running analyses across all operators
//	@Description	Aggregates the listing endpoints of all configured operators
//	@Description	and returns combined resource info. Errors for individual
//	@Description	operators are logged but do not prevent partial results.
//	@Produce		json
//	@Success		200	{object}	reporting.ResourceInfo
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/operator-listing [get]
func (h *HTTPHandlers) AdminOperatorListingHandler(c echo.Context) error {
	ctx := c.Request().Context()

	merged := reporting.NewResourceInfo()

	if h.scheduler == nil {
		// No operators configured; return empty resource info.
		return c.JSON(http.StatusOK, merged)
	}

	// Query all operators in parallel and collect results.
	clients := h.scheduler.Clients()
	type result struct {
		info *reporting.ResourceInfo
		name string
		err  error
	}
	results := make([]result, len(clients))

	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Go(func() {
			info, err := client.Listing(ctx)
			results[i] = result{info: info, name: client.Name(), err: err}
		})
	}
	wg.Wait()

	type operatorError struct {
		Operator string `json:"operator"`
		Error    string `json:"error"`
	}
	var errs []operatorError

	for _, r := range results {
		if r.err != nil {
			log.Errorf("error listing analyses from operator %s: %v", r.name, r.err)
			errs = append(errs, operatorError{Operator: r.name, Error: r.err.Error()})
			continue
		}
		merged.Deployments = append(merged.Deployments, r.info.Deployments...)
		merged.Pods = append(merged.Pods, r.info.Pods...)
		merged.ConfigMaps = append(merged.ConfigMaps, r.info.ConfigMaps...)
		merged.Services = append(merged.Services, r.info.Services...)
		merged.Ingresses = append(merged.Ingresses, r.info.Ingresses...)
		merged.Routes = append(merged.Routes, r.info.Routes...)
	}

	reporting.SortByCreationTime(merged)

	// Include operator errors in the response so clients know which
	// operators were unreachable and that results may be partial.
	resp := struct {
		reporting.ResourceInfo
		Errors []operatorError `json:"errors,omitempty"`
	}{
		ResourceInfo: *merged,
		Errors:       errs,
	}

	return c.JSON(http.StatusOK, resp)
}

// ListPodsResponse is the response body for the pods listing endpoint.
type ListPodsResponse struct {
	Pods []incluster.RetPod `json:"pods"`
}

// PodsHandler lists the K8s pods associated with the given analysis ID for the
// requesting user.
//
//	@ID				list-pods
//	@Summary		Lists the k8s pods associated with the provided external-id
//	@Description	Lists the k8s pods associated with the provided external-id. For now
//	@Description	just returns pod info in the format `{"pods" : [{}]}`
//	@Produce		json
//	@Param			analysis-id	path		string	true	"Analysis ID"
//	@Param			user		query		string	true	"Username"
//	@Success		200			{object}	ListPodsResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		403			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/vice/{analysis-id}/pods [get]
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
