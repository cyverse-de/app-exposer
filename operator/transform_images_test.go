package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// stubRewriter satisfies ImageRewriter for tests without pulling in a full
// ManualMirrorImageCacheManager.
type stubRewriter struct{ m map[string]string }

func (s stubRewriter) RewriteImage(image string) (string, bool) {
	out, ok := s.m[image]
	if !ok {
		return image, false
	}
	return out, true
}

func makeDeploymentWithImages(init, main []string) *appsv1.Deployment {
	initContainers := make([]apiv1.Container, len(init))
	for i, img := range init {
		initContainers[i] = apiv1.Container{Name: "init-" + img, Image: img}
	}
	containers := make([]apiv1.Container, len(main))
	for i, img := range main {
		containers[i] = apiv1.Container{Name: "main-" + img, Image: img}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: appsv1.DeploymentSpec{
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					InitContainers: initContainers,
					Containers:     containers,
				},
			},
		},
	}
}

func TestTransformImageRefs(t *testing.T) {
	rewriter := stubRewriter{m: map[string]string{
		"harbor.cyverse.org/de/vice-proxy:latest": "ecr/vice-proxy:latest",
		"harbor.cyverse.org/de/porklock:latest":   "ecr/porklock:latest",
	}}

	tests := []struct {
		name           string
		initIn, mainIn []string
		wantInit, wantMain []string
		wantCount      int
	}{
		{
			name: "no images match: deployment unchanged",
			initIn: []string{"harbor.cyverse.org/de/unrelated:1"},
			mainIn: []string{"harbor.cyverse.org/de/also-unrelated:2"},
			wantInit: []string{"harbor.cyverse.org/de/unrelated:1"},
			wantMain: []string{"harbor.cyverse.org/de/also-unrelated:2"},
			wantCount: 0,
		},
		{
			name: "init container is rewritten",
			initIn: []string{"harbor.cyverse.org/de/porklock:latest"},
			mainIn: []string{"harbor.cyverse.org/de/vice-app:latest"},
			wantInit: []string{"ecr/porklock:latest"},
			wantMain: []string{"harbor.cyverse.org/de/vice-app:latest"},
			wantCount: 1,
		},
		{
			name: "main container is rewritten",
			initIn: []string{},
			mainIn: []string{"harbor.cyverse.org/de/vice-proxy:latest"},
			wantInit: []string{},
			wantMain: []string{"ecr/vice-proxy:latest"},
			wantCount: 1,
		},
		{
			name: "both kinds of containers rewritten in one pass",
			initIn: []string{"harbor.cyverse.org/de/porklock:latest"},
			mainIn: []string{"harbor.cyverse.org/de/vice-proxy:latest", "harbor.cyverse.org/de/unrelated:3"},
			wantInit: []string{"ecr/porklock:latest"},
			wantMain: []string{"ecr/vice-proxy:latest", "harbor.cyverse.org/de/unrelated:3"},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dep := makeDeploymentWithImages(tt.initIn, tt.mainIn)
			got := TransformImageRefs(dep, rewriter)
			assert.Equal(t, tt.wantCount, got)

			gotInit := make([]string, 0, len(dep.Spec.Template.Spec.InitContainers))
			for _, c := range dep.Spec.Template.Spec.InitContainers {
				gotInit = append(gotInit, c.Image)
			}
			gotMain := make([]string, 0, len(dep.Spec.Template.Spec.Containers))
			for _, c := range dep.Spec.Template.Spec.Containers {
				gotMain = append(gotMain, c.Image)
			}
			assert.Equal(t, tt.wantInit, gotInit, "init container images")
			assert.Equal(t, tt.wantMain, gotMain, "main container images")
		})
	}
}

func TestTransformImageRefsNilSafe(t *testing.T) {
	rewriter := stubRewriter{m: map[string]string{"a": "b"}}
	require.Equal(t, 0, TransformImageRefs(nil, rewriter), "nil deployment is a no-op")
	dep := makeDeploymentWithImages(nil, []string{"a"})
	require.Equal(t, 0, TransformImageRefs(dep, nil), "nil rewriter is a no-op")
	assert.Equal(t, "a", dep.Spec.Template.Spec.Containers[0].Image)
}
