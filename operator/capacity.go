package operator

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/cyverse-de/app-exposer/operatorclient"
)

// CapacityCalculator computes the current resource capacity and usage for the
// operator's cluster by querying K8s nodes and VICE deployments.
type CapacityCalculator struct {
	clientset         kubernetes.Interface
	namespace         string
	maxAnalyses       int
	nodeLabelSelector string
}

// NewCapacityCalculator creates a new CapacityCalculator. Panics if required
// dependencies are nil or invalid, since these indicate programmer error.
func NewCapacityCalculator(
	clientset kubernetes.Interface,
	namespace string,
	maxAnalyses int,
	nodeLabelSelector string,
) *CapacityCalculator {
	if clientset == nil {
		panic("capacity: clientset must not be nil")
	}
	if namespace == "" {
		panic("capacity: namespace must not be empty")
	}
	return &CapacityCalculator{
		clientset:         clientset,
		namespace:         namespace,
		maxAnalyses:       maxAnalyses,
		nodeLabelSelector: nodeLabelSelector,
	}
}

// Calculate queries K8s for node resources and running VICE deployments,
// returning a CapacityResponse describing current cluster utilization.
func (cc *CapacityCalculator) Calculate(ctx context.Context) (*operatorclient.CapacityResponse, error) {
	// List schedulable nodes, optionally filtered by label selector.
	nodeOpts := metav1.ListOptions{}
	if cc.nodeLabelSelector != "" {
		nodeOpts.LabelSelector = cc.nodeLabelSelector
	}

	nodes, err := cc.clientset.CoreV1().Nodes().List(ctx, nodeOpts)
	if err != nil {
		return nil, err
	}

	var allocatableCPU, allocatableMemory int64
	for _, node := range nodes.Items {
		if node.Spec.Unschedulable {
			continue
		}
		cpu := node.Status.Allocatable.Cpu()
		if cpu != nil {
			allocatableCPU += cpu.MilliValue()
		}
		mem := node.Status.Allocatable.Memory()
		if mem != nil {
			allocatableMemory += mem.Value()
		}
	}

	// Count running VICE deployments by the app-type=interactive label.
	viceSelector := labels.Set{"app-type": "interactive"}.AsSelector().String()
	deps, err := cc.clientset.AppsV1().Deployments(cc.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: viceSelector,
	})
	if err != nil {
		return nil, err
	}

	runningAnalyses := len(deps.Items)

	// Sum resource requests from running VICE pods.
	var usedCPU, usedMemory int64
	for _, dep := range deps.Items {
		for _, container := range dep.Spec.Template.Spec.Containers {
			cpuReq := container.Resources.Requests.Cpu()
			if cpuReq != nil {
				usedCPU += cpuReq.MilliValue()
			}
			memReq := container.Resources.Requests.Memory()
			if memReq != nil {
				usedMemory += memReq.Value()
			}
		}
	}

	// When maxAnalyses is 0, the limit is disabled (e.g. for autoscaling
	// clusters). AvailableSlots=-1 signals "unlimited" to the scheduler.
	available := -1
	if cc.maxAnalyses > 0 {
		available = cc.maxAnalyses - runningAnalyses
		if available < 0 {
			available = 0
		}
	}

	return &operatorclient.CapacityResponse{
		MaxAnalyses:       cc.maxAnalyses,
		RunningAnalyses:   runningAnalyses,
		AvailableSlots:    available,
		AllocatableCPU:    allocatableCPU,
		AllocatableMemory: allocatableMemory,
		UsedCPU:           usedCPU,
		UsedMemory:        usedMemory,
	}, nil
}
