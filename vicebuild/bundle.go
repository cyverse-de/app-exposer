package vicebuild

import (
	"github.com/cyverse-de/app-exposer/operatorclient"
	apiv1 "k8s.io/api/core/v1"
)

// BuildBundle assembles every Kubernetes object for a VICE analysis from the
// spec and this cluster's Config. The result is the same AnalysisBundle shape
// app-exposer used to build and ship — but with cluster-correct values baked in
// at construction, so no post-hoc transform pass is required. This is the
// operator-side construction entry point.
//
// The returned bundle reuses operatorclient.AnalysisBundle as the in-memory
// container so the operator's existing applyBundle/egress/labeling code can
// consume it unchanged; nothing here is serialized back over the wire.
func (c *Config) BuildBundle(spec *operatorclient.VICESpec) (*operatorclient.AnalysisBundle, error) {
	pvs, err := c.persistentVolumes(spec)
	if err != nil {
		return nil, err
	}

	return &operatorclient.AnalysisBundle{
		AnalysisID: spec.AnalysisID,
		Deployment: c.Deployment(spec),
		Service:    c.Service(spec),
		HTTPRoute:  c.HTTPRoute(spec),
		ConfigMaps: []*apiv1.ConfigMap{
			c.ExcludesConfigMap(spec),
			c.PermissionsConfigMap(spec),
			c.InputPathListConfigMap(spec),
		},
		PersistentVolumes:      pvs,
		PersistentVolumeClaims: c.volumeClaims(spec),
		PodDisruptionBudget:    c.PodDisruptionBudget(spec),
	}, nil
}
