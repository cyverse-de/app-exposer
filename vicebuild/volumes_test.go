package vicebuild

import (
	"encoding/json"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
)

// TestWorkingDirStorageClassFolded confirms TransformWorkingDirStorageClass is
// folded in: the working-dir PVC carries the cluster's storage class, or none
// (cluster default) when unset, while the CSI claim keeps its own class.
func TestWorkingDirStorageClassFolded(t *testing.T) {
	t.Run("storage class set", func(t *testing.T) {
		cfg := testConfig()
		cfg.LocalStorageClass = "gp3"
		claims := cfg.volumeClaims(testSpec())
		wd := claims[0]
		require.NotNil(t, wd.Spec.StorageClassName)
		assert.Equal(t, "gp3", *wd.Spec.StorageClassName)
	})
	t.Run("empty means cluster default", func(t *testing.T) {
		cfg := testConfig()
		cfg.LocalStorageClass = ""
		claims := cfg.volumeClaims(testSpec())
		assert.Nil(t, claims[0].Spec.StorageClassName, "unset storage class lets the cluster default apply")
	})
}

func TestVolumeClaimsCSI(t *testing.T) {
	cfg := testConfig()
	cfg.UseCSIDriver = true
	claims := cfg.volumeClaims(testSpec())
	require.Len(t, claims, 2)
	assert.Equal(t, "working-dir-external-1", claims[0].Name)
	assert.Equal(t, "csi-data-volume-claim-external-1", claims[1].Name)
	require.NotNil(t, claims[1].Spec.StorageClassName)
	assert.Equal(t, constants.CSIDriverStorageClassName, *claims[1].Spec.StorageClassName)
}

func TestPersistentVolumesCSIDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.UseCSIDriver = false
	pvs, err := cfg.persistentVolumes(testSpec())
	require.NoError(t, err)
	assert.Nil(t, pvs)
}

// TestPersistentVolumesCSIMappings confirms the CSI PV encodes the full input
// list plus output, home, and shared mappings, and uses the bare proxy
// username.
func TestPersistentVolumesCSIMappings(t *testing.T) {
	cfg := testConfig()
	cfg.UseCSIDriver = true
	cfg.IRODSZone = "iplant"
	spec := testSpec()
	spec.Submitter = "someuser@iplantcollaborative.org"
	spec.OutputDirectory = "/iplant/home/someuser/out"
	spec.Inputs = []operatorclient.InputSpec{
		{IRODSPath: "/iplant/home/someuser/in.txt", Type: "fileinput"},
		{IRODSPath: "/iplant/home/someuser/dir/", Type: "folderinput"},
	}

	pvs, err := cfg.persistentVolumes(spec)
	require.NoError(t, err)
	require.Len(t, pvs, 1)
	attrs := pvs[0].Spec.CSI.VolumeAttributes
	assert.Equal(t, "someuser", attrs["clientUser"], "CSI proxy user is the bare username")

	var mappings []IRODSFSPathMapping
	require.NoError(t, json.Unmarshal([]byte(attrs["path_mapping_json"]), &mappings))
	// 2 inputs + output + home + shared = 5
	require.Len(t, mappings, 5)
	assert.Equal(t, "file", mappings[0].ResourceType)
	assert.Equal(t, "dir", mappings[1].ResourceType)
}

func TestPersistentVolumeCapacity(t *testing.T) {
	tests := []struct {
		name      string
		diskBytes int64
		wantMin   int64
	}{
		{name: "below default floors at 5Gi", diskBytes: 1 << 20, wantMin: 5 << 30},
		{name: "above default uses ask", diskBytes: 20 << 30, wantMin: 20 << 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := testSpec()
			spec.Resources.MinDiskBytes = tt.diskBytes
			q := persistentVolumeCapacity(spec)
			assert.Equal(t, tt.wantMin, q.Value())
		})
	}
}

// TestInputPathListVolumePresence confirms the input-path-list volume only
// appears when there are ticketless inputs.
func TestInputPathListVolumePresence(t *testing.T) {
	cfg := testConfig()
	cfg.UseCSIDriver = true

	t.Run("no ticketless inputs", func(t *testing.T) {
		vols := cfg.podVolumes(testSpec())
		assert.NotContains(t, volumeNames(vols), constants.InputPathListVolumeName)
	})
	t.Run("with ticketless inputs", func(t *testing.T) {
		spec := testSpec()
		spec.InputPathListPaths = []string{"/iplant/home/someuser/in.txt"}
		vols := cfg.podVolumes(spec)
		assert.Contains(t, volumeNames(vols), constants.InputPathListVolumeName)
	})
}

func volumeNames(vols []apiv1.Volume) []string {
	out := make([]string, 0, len(vols))
	for _, v := range vols {
		out = append(out, v.Name)
	}
	return out
}
