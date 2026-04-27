package operator

import (
	"context"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCapacityCalculator(t *testing.T) {
	tests := []struct {
		name              string
		nodes             []apiv1.Node
		deployments       []appsv1.Deployment
		maxAnalyses       int
		nodeLabelSelector string
		wantRunning       int
		wantAvailable     int
		wantErr           bool
	}{
		{
			name: "empty cluster has full capacity",
			nodes: []apiv1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: apiv1.NodeStatus{
						Allocatable: apiv1.ResourceList{
							apiv1.ResourceCPU:    resource.MustParse("4"),
							apiv1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
			maxAnalyses:   10,
			wantRunning:   0,
			wantAvailable: 10,
		},
		{
			name: "running analyses reduce available slots",
			nodes: []apiv1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: apiv1.NodeStatus{
						Allocatable: apiv1.ResourceList{
							apiv1.ResourceCPU:    resource.MustParse("4"),
							apiv1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
			deployments: []appsv1.Deployment{
				makeVICEDeployment("dep1", "100m", "256Mi"),
				makeVICEDeployment("dep2", "200m", "512Mi"),
			},
			maxAnalyses:   5,
			wantRunning:   2,
			wantAvailable: 3,
		},
		{
			name: "at capacity returns zero available",
			nodes: []apiv1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: apiv1.NodeStatus{
						Allocatable: apiv1.ResourceList{
							apiv1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
			deployments: []appsv1.Deployment{
				makeVICEDeployment("dep1", "1", "256Mi"),
				makeVICEDeployment("dep2", "1", "256Mi"),
			},
			maxAnalyses:   2,
			wantRunning:   2,
			wantAvailable: 0,
		},
		{
			name: "unschedulable nodes are excluded",
			nodes: []apiv1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Spec:       apiv1.NodeSpec{Unschedulable: true},
					Status: apiv1.NodeStatus{
						Allocatable: apiv1.ResourceList{
							apiv1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node2"},
					Status: apiv1.NodeStatus{
						Allocatable: apiv1.ResourceList{
							apiv1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
			maxAnalyses:   10,
			wantRunning:   0,
			wantAvailable: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset()
			ctx := context.Background()

			// Create nodes.
			for i := range tt.nodes {
				_, err := clientset.CoreV1().Nodes().Create(ctx, &tt.nodes[i], metav1.CreateOptions{})
				require.NoError(t, err)
			}

			// Create VICE deployments.
			for i := range tt.deployments {
				_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &tt.deployments[i], metav1.CreateOptions{})
				require.NoError(t, err)
			}

			calc, err := NewCapacityCalculator(clientset, "vice-apps", tt.maxAnalyses, tt.nodeLabelSelector)
			require.NoError(t, err)
			cap, err := calc.Calculate(ctx)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantRunning, cap.RunningAnalyses)
			assert.Equal(t, tt.wantAvailable, cap.AvailableSlots)
			assert.Equal(t, tt.maxAnalyses, cap.MaxAnalyses)
		})
	}
}

// makeVICEDeployment creates a minimal VICE deployment for testing.
func makeVICEDeployment(name, cpu, memory string) appsv1.Deployment {
	return appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "vice-apps",
			Labels:    map[string]string{constants.AppTypeLabel: "interactive"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:  "analysis",
							Image: "test:latest",
							Resources: apiv1.ResourceRequirements{
								Requests: apiv1.ResourceList{
									apiv1.ResourceCPU:    resource.MustParse(cpu),
									apiv1.ResourceMemory: resource.MustParse(memory),
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestCapacityCalculatorUnlimited(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&apiv1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node1"},
			Status: apiv1.NodeStatus{
				Allocatable: apiv1.ResourceList{
					apiv1.ResourceCPU:    resource.MustParse("4"),
					apiv1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
	)

	// maxAnalyses=0 means unlimited.
	calc, err := NewCapacityCalculator(cs, "vice-apps", 0, "")
	require.NoError(t, err)
	cap, err := calc.Calculate(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, cap.MaxAnalyses)
	assert.Equal(t, 0, cap.RunningAnalyses)
	assert.Equal(t, -1, cap.AvailableSlots, "unlimited mode should report -1 available slots")
	assert.Greater(t, cap.AllocatableCPU, int64(0), "should still report allocatable CPU")
}
