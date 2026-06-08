package vicebuild

import (
	"fmt"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
)

// Resource names are all derived from the analysis's ExternalID (a.k.a.
// InvocationID), matching the incluster builders so cleanup-by-label and the
// status/exit paths keep finding the same objects.

func deploymentName(spec *operatorclient.VICESpec) string { return string(spec.ExternalID) }
func routeName(spec *operatorclient.VICESpec) string      { return string(spec.ExternalID) }
func pdbName(spec *operatorclient.VICESpec) string        { return string(spec.ExternalID) }
func serviceName(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("vice-%s", spec.ExternalID)
}
func excludesConfigMapName(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("%s-%s", constants.ExcludesFileName, spec.ExternalID)
}
func permissionsConfigMapName(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("%s-%s", constants.PermissionsConfigMapPrefix, spec.ExternalID)
}
func inputPathListConfigMapName(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("%s-%s", constants.InputPathListFileName, spec.ExternalID)
}
func workingDirVolumeName(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("%s-%s", constants.WorkingDirVolumeName, spec.ExternalID)
}
func csiDataVolumeName(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("%s-%s", constants.CSIDriverDataVolumeNamePrefix, spec.ExternalID)
}
func csiDataVolumeClaimName(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("%s-%s", constants.CSIDriverDataVolumeClaimNamePrefix, spec.ExternalID)
}
func csiDataVolumeHandle(spec *operatorclient.VICESpec) string {
	return fmt.Sprintf("%s-handle-%s", constants.CSIDriverDataVolumeNamePrefix, spec.ExternalID)
}
