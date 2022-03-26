package internal

import (
	"context"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
)

// AsyncDataHandler returns data that is generately asynchronously from the job launch.
func (i *Internal) AsyncDataHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.QueryParam("external-id")
	if externalID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "external-id not set")
	}

	analysisID, err := i.apps.GetAnalysisIDByExternalID(externalID)
	if err != nil {
		log.Error(err)
		return err
	}

	filter := map[string]string{
		"external-id": externalID,
	}

	deployments, err := i.deploymentList(ctx, i.ViceNamespace, filter, []string{})
	if err != nil {
		return err
	}

	if len(deployments.Items) < 1 {
		return echo.NewHTTPError(http.StatusNotFound, "no deployments found.")
	}

	labels := deployments.Items[0].GetLabels()
	userID := labels["user-id"]

	subdomain := IngressName(userID, externalID)
	ipAddr, err := i.apps.GetUserIP(userID)
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

// getExternalID returns the externalID associated with the analysisID. For now,
// only returns the first result, since VICE analyses only have a single step in
// the database.
func (i *Internal) getExternalIDByAnalysisID(ctx context.Context, analysisID string) (string, error) {
	username, _, err := i.apps.GetUserByAnalysisID(analysisID)
	if err != nil {
		return "", err
	}

	log.Infof("username %s", username)

	externalIDs, err := i.getExternalIDs(ctx, username, analysisID)
	if err != nil {
		return "", err
	}

	if len(externalIDs) == 0 {
		return "", fmt.Errorf("no external-id found for analysis-id %s", analysisID)
	}

	// For now, just use the first external ID
	externalID := externalIDs[0]
	return externalID, nil
}
