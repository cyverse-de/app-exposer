package incluster

import (
	"context"

	"github.com/cyverse-de/app-exposer/incluster/httproutes"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/model/v10"
)

// BuildAnalysisBundle assembles all K8s resource objects for a VICE analysis
// into a serializable bundle ready to be sent to an operator.
func (i *Incluster) BuildAnalysisBundle(ctx context.Context, job *model.Job, analysisID string) (*operatorclient.AnalysisBundle, error) {
	bundle := &operatorclient.AnalysisBundle{
		AnalysisID: analysisID,
	}

	// Build the excludes ConfigMap (always present).
	excludesCM, err := i.excludesConfigMap(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.ConfigMaps = append(bundle.ConfigMaps, excludesCM)

	// Build the permissions ConfigMap (owner-only at launch time).
	permsCM, err := i.permissionsConfigMap(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.ConfigMaps = append(bundle.ConfigMaps, permsCM)

	// Build the input path list ConfigMap (present when there are inputs without tickets).
	inputCM, err := i.inputPathListConfigMap(ctx, job)
	if err != nil {
		return nil, err
	}
	if inputCM != nil {
		bundle.ConfigMaps = append(bundle.ConfigMaps, inputCM)
	}

	// Build the Deployment.
	deployment, err := i.GetDeployment(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.Deployment = deployment

	// Build the Service.
	svc, err := i.getService(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.Service = svc

	// Build the HTTPRoute using the gateway API provider.
	routeBuilder := httproutes.NewHTTPRouteBuilder(
		i.GatewayProvider,
		i.VICEBackendNamespace,
		i.ViceNamespace,
		i.ViceDomain,
		i.jobInfo,
	)
	httpRoute, err := routeBuilder.BuildRoute(ctx, job, svc)
	if err != nil {
		return nil, err
	}
	bundle.HTTPRoute = httpRoute

	// Build PersistentVolumes (may be nil/empty when CSI is disabled).
	pvs, err := i.getPersistentVolumes(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.PersistentVolumes = pvs

	// Build PersistentVolumeClaims.
	pvcs, err := i.getVolumeClaims(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.PersistentVolumeClaims = pvcs

	// Build the PodDisruptionBudget.
	pdb, err := i.createPodDisruptionBudget(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.PodDisruptionBudget = pdb

	return bundle, nil
}
