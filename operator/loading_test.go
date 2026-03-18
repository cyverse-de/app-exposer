package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
)

func TestComputeStage(t *testing.T) {
	tests := []struct {
		name      string
		pods      []apiv1.Pod
		depReady  bool
		svcExists bool
		wantStage string
		wantError string
	}{
		{
			name:      "no pods returns deploying",
			pods:      nil,
			depReady:  false,
			svcExists: false,
			wantStage: StageDeploying,
		},
		{
			name: "pending pods returns deploying",
			pods: []apiv1.Pod{
				{Status: apiv1.PodStatus{Phase: apiv1.PodPending}},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageDeploying,
		},
		{
			name: "running pods not ready returns starting",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: false},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageStarting,
		},
		{
			name: "all ready returns almost-ready when dep not ready",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						Conditions: []apiv1.PodCondition{
							{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
						},
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: true},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageAlmostReady,
		},
		{
			name: "dep ready and svc exists returns ready",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						Conditions: []apiv1.PodCondition{
							{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
						},
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: true},
						},
					},
				},
			},
			depReady:  true,
			svcExists: true,
			wantStage: StageReady,
		},
		{
			name: "crashloopbackoff returns error",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						ContainerStatuses: []apiv1.ContainerStatus{
							{
								Name:         "analysis",
								Ready:        false,
								RestartCount: 3,
								State: apiv1.ContainerState{
									Waiting: &apiv1.ContainerStateWaiting{
										Reason: "CrashLoopBackOff",
									},
								},
							},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageError,
			wantError: "container \"analysis\" is in CrashLoopBackOff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage, errMsg := computeStage(tt.pods, tt.depReady, tt.svcExists)
			assert.Equal(t, tt.wantStage, stage)
			if tt.wantError != "" {
				assert.Contains(t, errMsg, tt.wantError)
			}
		})
	}
}
