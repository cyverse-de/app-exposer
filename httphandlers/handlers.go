package httphandlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/cyverse-de/app-exposer/adapter"
	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/labstack/echo/v4"
	"k8s.io/client-go/kubernetes"
)

var log = common.Log

var otelName = "github.com/cyverse-de/app-exposer/handlers"

type HTTPHandlers struct {
	incluster *incluster.Incluster
	apps      *apps.Apps
	clientset kubernetes.Interface
	//db           *db.Database
	batchadapter *adapter.JEXAdapter
}

func New(incluster *incluster.Incluster, apps *apps.Apps, clientset kubernetes.Interface, batchadapter *adapter.JEXAdapter) *HTTPHandlers {
	return &HTTPHandlers{
		incluster,
		apps,
		clientset,
		//db,
		batchadapter,
	}
}

// AdminGetExternalIDHandler returns the external ID associated with the analysis ID.
// There is only one external ID for each VICE analysis, unlike non-VICE analyses.
func (h *HTTPHandlers) AdminGetExternalIDHandler(c echo.Context) error {
	var (
		err        error
		analysisID string
		externalID string
	)

	ctx := c.Request().Context()

	// analysisID is required
	analysisID = c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	externalID, err = h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	outputMap := map[string]string{
		"externalID": externalID,
	}

	return c.JSON(http.StatusOK, outputMap)
}

// ApplyAsyncLabelsHandler is the http handler for triggering the application
// of labels on running VICE analyses.
func (h *HTTPHandlers) ApplyAsyncLabelsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	errs := h.incluster.ApplyAsyncLabels(ctx)

	if len(errs) > 0 {
		var errMsg strings.Builder
		for _, err := range errs {
			log.Error(err)
			fmt.Fprintf(&errMsg, "%s\n", err.Error())
		}

		return c.String(http.StatusInternalServerError, errMsg.String())
	}
	return c.NoContent(http.StatusOK)
}

// AsyncDataHandler returns data that is generately asynchronously from the job launch.
func (h *HTTPHandlers) AsyncDataHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.QueryParam("external-id")
	if externalID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "external-id not set")
	}

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		log.Error(err)
		return err
	}

	filter := map[string]string{
		"external-id": externalID,
	}

	deployments, err := h.incluster.DeploymentList(ctx, h.incluster.ViceNamespace, filter, []string{})
	if err != nil {
		return err
	}

	if len(deployments.Items) < 1 {
		return echo.NewHTTPError(http.StatusNotFound, "no deployments found.")
	}

	labels := deployments.Items[0].GetLabels()
	userID := labels["user-id"]

	subdomain := incluster.IngressName(userID, externalID)
	ipAddr, err := h.apps.GetUserIP(ctx, userID)
	if err != nil {
		log.Error(err)
		return err
	}

	return c.JSON(http.StatusOK, map[string]string{
		"analysisID": analysisID,
		"subdomain":  subdomain,
		"ipAddr":     ipAddr,
	})
}
