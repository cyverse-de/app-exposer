package httphandlers

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

// FilterableDeploymentsHandler lists all of the deployments.
func (h *HTTPHandlers) FilterableDeploymentsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	deployments, err := h.incluster.GetFilteredDeployments(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.DeploymentInfo{
		"deployments": deployments,
	})
}

// FilterablePodsHandler returns a listing of the pods in a VICE analysis.
func (h *HTTPHandlers) FilterablePodsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	pods, err := h.incluster.GetFilteredPods(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.PodInfo{
		"pods": pods,
	})
}

// FilterableConfigMapsHandler lists configmaps in use by VICE apps.
func (h *HTTPHandlers) FilterableConfigMapsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	cms, err := h.incluster.GetFilteredConfigMaps(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.ConfigMapInfo{
		"configmaps": cms,
	})
}

// FilterableServicesHandler lists services in use by VICE apps.
func (h *HTTPHandlers) FilterableServicesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	svcs, err := h.incluster.GetFilteredServices(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.ServiceInfo{
		"services": svcs,
	})
}

// FilterableIngressesHandler lists ingresses in use by VICE apps.
func (h *HTTPHandlers) FilterableIngressesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	ingresses, err := h.incluster.GetFilteredIngresses(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.IngressInfo{
		"ingresses": ingresses,
	})
}

// AdminDescribeAnalysisHandler returns a listing entry for a single analysis
// asssociated with the host/subdomain passed in as 'host' from the URL.
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

// DescribeAnalysisHandler returns a listing entry for a single analysis associated
// with the host/subdomain passed in as 'host' from the URL.
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

// FilterableResourcesHandler returns all of the k8s resources associated with a VICE analysis
// but checks permissions to see if the requesting user has permission to access the resource.
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

// AdminFilterableResourcesHandler returns all of the k8s resources associated with a VICE analysis.
func (h *HTTPHandlers) AdminFilterableResourcesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, listing)
}

// PodsHandler lists the k8s pods associated with the provided external-id. For now
// just returns pod info in the format `{"pods" : [{}]}`
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
		return fmt.Errorf("no external-id found for analysis-id %s", analysisID)
	}

	// For now, just use the first external ID
	externalID := externalIDs[0]

	returnedPods, err := h.incluster.GetPods(ctx, externalID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.RetPod{
		"pods": returnedPods,
	})
}
