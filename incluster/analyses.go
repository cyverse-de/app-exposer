package incluster

import (
	"context"
	"fmt"
)

// getExternalID returns the externalID associated with the analysisID. For now,
// only returns the first result, since VICE analyses only have a single step in
// the database.
func (i *Incluster) GetExternalIDByAnalysisID(ctx context.Context, analysisID string) (string, error) {
	username, _, err := i.apps.GetUserByAnalysisID(ctx, analysisID)
	if err != nil {
		return "", err
	}

	log.Infof("username %s", username)

	externalIDs, err := i.GetExternalIDs(ctx, username, analysisID)
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
