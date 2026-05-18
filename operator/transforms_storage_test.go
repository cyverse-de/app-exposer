package operator

import (
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTransformWorkingDirStorageClass(t *testing.T) {
	const (
		workingDirName   = constants.WorkingDirVolumeName + "-abc123"
		csiClaimName     = constants.CSIDriverDataVolumeClaimNamePrefix + "-abc123"
		unrelatedName    = "unrelated-pvc-abc123"
		preExistingIRODS = "irods-sc"
	)

	newBundle := func() *operatorclient.AnalysisBundle {
		irods := preExistingIRODS
		return &operatorclient.AnalysisBundle{
			PersistentVolumeClaims: []*apiv1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: workingDirName},
					Spec:       apiv1.PersistentVolumeClaimSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: csiClaimName},
					Spec: apiv1.PersistentVolumeClaimSpec{
						StorageClassName: &irods,
						VolumeName:       "csi-data-volume-abc123",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: unrelatedName},
					Spec:       apiv1.PersistentVolumeClaimSpec{},
				},
				nil,
			},
		}
	}

	tests := []struct {
		name         string
		storageClass string
		wantWorking  *string
	}{
		{
			name:         "empty storage class leaves working-dir PVC alone",
			storageClass: "",
			wantWorking:  nil,
		},
		{
			name:         "configured storage class is applied to working-dir PVC",
			storageClass: "gp3",
			wantWorking:  strPtr("gp3"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := newBundle()
			TransformWorkingDirStorageClass(bundle, tt.storageClass)

			// working-dir PVC: rewritten when configured, untouched when empty.
			if tt.wantWorking == nil {
				assert.Nil(t, bundle.PersistentVolumeClaims[0].Spec.StorageClassName,
					"working-dir PVC should be left alone when storageClass is empty")
			} else {
				if assert.NotNil(t, bundle.PersistentVolumeClaims[0].Spec.StorageClassName) {
					assert.Equal(t, *tt.wantWorking, *bundle.PersistentVolumeClaims[0].Spec.StorageClassName)
				}
			}

			// iRODS CSI PVC: never touched.
			if assert.NotNil(t, bundle.PersistentVolumeClaims[1].Spec.StorageClassName) {
				assert.Equal(t, preExistingIRODS, *bundle.PersistentVolumeClaims[1].Spec.StorageClassName,
					"iRODS CSI PVC must keep its irods-sc storage class")
			}
			assert.Equal(t, "csi-data-volume-abc123", bundle.PersistentVolumeClaims[1].Spec.VolumeName,
				"iRODS CSI PVC volume binding must be preserved")

			// Other PVCs not matching the working-dir prefix: never touched.
			assert.Nil(t, bundle.PersistentVolumeClaims[2].Spec.StorageClassName,
				"unrelated PVC must not be modified")
		})
	}
}

func TestTransformWorkingDirStorageClass_NilBundle(t *testing.T) {
	// Must not panic on a nil bundle.
	TransformWorkingDirStorageClass(nil, "gp3")
}

func strPtr(s string) *string { return &s }
