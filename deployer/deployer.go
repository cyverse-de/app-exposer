// Package deployer provides the core logic for creating and managing
// Kubernetes resources for VICE deployments. This package is used by
// both the standalone deployer service and the AWS Lambda handler.
package deployer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/vicetypes"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

var log = common.Log

// Version is the deployer version, set at build time
var Version = "dev"

// Deployer handles the creation and management of Kubernetes resources
// for VICE deployments. It is stateless and operates on the specifications
// provided by the coordinator.
type Deployer struct {
	clientset kubernetes.Interface
	namespace string
}

// New creates a new Deployer instance.
func New(clientset kubernetes.Interface, namespace string) *Deployer {
	return &Deployer{
		clientset: clientset,
		namespace: namespace,
	}
}

// CreateDeployment creates all Kubernetes resources specified in the deployment spec.
// Resources are created in order: ConfigMaps, PVs, PVCs, Deployment, PDB, Service, Ingress.
func (d *Deployer) CreateDeployment(ctx context.Context, spec *vicetypes.VICEDeploymentSpec) (*vicetypes.DeploymentResponse, error) {
	var resourcesCreated []string
	externalID := spec.Metadata.ExternalID
	namespace := spec.Metadata.Namespace
	if namespace == "" {
		namespace = d.namespace
	}

	log.Infof("creating deployment for external-id %s in namespace %s", externalID, namespace)

	// 1. Create ConfigMaps
	for _, cm := range spec.ConfigMaps {
		if err := d.upsertConfigMap(ctx, namespace, cm); err != nil {
			return &vicetypes.DeploymentResponse{
				Status:           "error",
				ExternalID:       externalID,
				ResourcesCreated: resourcesCreated,
				Error:            fmt.Sprintf("failed to create configmap %s: %v", cm.Name, err),
			}, err
		}
		resourcesCreated = append(resourcesCreated, "configmap:"+cm.Name)
	}

	// 2. Create PersistentVolumes (if any)
	for _, pv := range spec.PersistentVolumes {
		if err := d.upsertPersistentVolume(ctx, pv); err != nil {
			return &vicetypes.DeploymentResponse{
				Status:           "error",
				ExternalID:       externalID,
				ResourcesCreated: resourcesCreated,
				Error:            fmt.Sprintf("failed to create persistentvolume %s: %v", pv.Name, err),
			}, err
		}
		resourcesCreated = append(resourcesCreated, "persistentvolume:"+pv.Name)
	}

	// 3. Create PersistentVolumeClaims
	for _, pvc := range spec.PersistentVolumeClaims {
		if err := d.upsertPersistentVolumeClaim(ctx, namespace, pvc); err != nil {
			return &vicetypes.DeploymentResponse{
				Status:           "error",
				ExternalID:       externalID,
				ResourcesCreated: resourcesCreated,
				Error:            fmt.Sprintf("failed to create persistentvolumeclaim %s: %v", pvc.Name, err),
			}, err
		}
		resourcesCreated = append(resourcesCreated, "persistentvolumeclaim:"+pvc.Name)
	}

	// 4. Create Deployment
	if spec.Deployment != nil {
		if err := d.upsertDeployment(ctx, namespace, spec.Deployment); err != nil {
			return &vicetypes.DeploymentResponse{
				Status:           "error",
				ExternalID:       externalID,
				ResourcesCreated: resourcesCreated,
				Error:            fmt.Sprintf("failed to create deployment %s: %v", spec.Deployment.Name, err),
			}, err
		}
		resourcesCreated = append(resourcesCreated, "deployment:"+spec.Deployment.Name)
	}

	// 5. Create PodDisruptionBudget
	if spec.PodDisruptionBudget != nil {
		if err := d.upsertPodDisruptionBudget(ctx, namespace, spec.PodDisruptionBudget); err != nil {
			return &vicetypes.DeploymentResponse{
				Status:           "error",
				ExternalID:       externalID,
				ResourcesCreated: resourcesCreated,
				Error:            fmt.Sprintf("failed to create poddisruptionbudget %s: %v", spec.PodDisruptionBudget.Name, err),
			}, err
		}
		resourcesCreated = append(resourcesCreated, "poddisruptionbudget:"+spec.PodDisruptionBudget.Name)
	}

	// 6. Create Service
	if spec.Service != nil {
		if err := d.upsertService(ctx, namespace, spec.Service); err != nil {
			return &vicetypes.DeploymentResponse{
				Status:           "error",
				ExternalID:       externalID,
				ResourcesCreated: resourcesCreated,
				Error:            fmt.Sprintf("failed to create service %s: %v", spec.Service.Name, err),
			}, err
		}
		resourcesCreated = append(resourcesCreated, "service:"+spec.Service.Name)
	}

	// 7. Create Ingress
	if spec.Ingress != nil {
		if err := d.upsertIngress(ctx, namespace, spec.Ingress); err != nil {
			return &vicetypes.DeploymentResponse{
				Status:           "error",
				ExternalID:       externalID,
				ResourcesCreated: resourcesCreated,
				Error:            fmt.Sprintf("failed to create ingress %s: %v", spec.Ingress.Name, err),
			}, err
		}
		resourcesCreated = append(resourcesCreated, "ingress:"+spec.Ingress.Name)
	}

	log.Infof("successfully created deployment for external-id %s", externalID)

	return &vicetypes.DeploymentResponse{
		Status:           "created",
		ExternalID:       externalID,
		ResourcesCreated: resourcesCreated,
	}, nil
}

// DeleteDeployment deletes all Kubernetes resources associated with the given external ID.
// Resources are deleted in reverse order: Ingress, Service, PDB, Deployment, PVCs, PVs, ConfigMaps.
func (d *Deployer) DeleteDeployment(ctx context.Context, externalID string, namespace string) (*vicetypes.DeploymentResponse, error) {
	if namespace == "" {
		namespace = d.namespace
	}

	var resourcesDeleted []string

	log.Infof("deleting deployment for external-id %s in namespace %s", externalID, namespace)

	labelSelector := labels.Set(map[string]string{
		"external-id": externalID,
	}).AsSelector().String()

	listOptions := metav1.ListOptions{
		LabelSelector: labelSelector,
	}

	// 1. Delete Ingresses
	ingressClient := d.clientset.NetworkingV1().Ingresses(namespace)
	ingressList, err := ingressClient.List(ctx, listOptions)
	if err != nil {
		log.Warnf("failed to list ingresses for %s: %v", externalID, err)
	} else {
		for _, ingress := range ingressList.Items {
			if err := ingressClient.Delete(ctx, ingress.Name, metav1.DeleteOptions{}); err != nil {
				log.Warnf("failed to delete ingress %s: %v", ingress.Name, err)
			} else {
				resourcesDeleted = append(resourcesDeleted, "ingress:"+ingress.Name)
			}
		}
	}

	// 2. Delete Services
	svcClient := d.clientset.CoreV1().Services(namespace)
	svcList, err := svcClient.List(ctx, listOptions)
	if err != nil {
		log.Warnf("failed to list services for %s: %v", externalID, err)
	} else {
		for _, svc := range svcList.Items {
			if err := svcClient.Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil {
				log.Warnf("failed to delete service %s: %v", svc.Name, err)
			} else {
				resourcesDeleted = append(resourcesDeleted, "service:"+svc.Name)
			}
		}
	}

	// 3. Delete PodDisruptionBudgets
	pdbClient := d.clientset.PolicyV1().PodDisruptionBudgets(namespace)
	pdbList, err := pdbClient.List(ctx, listOptions)
	if err != nil {
		log.Warnf("failed to list poddisruptionbudgets for %s: %v", externalID, err)
	} else {
		for _, pdb := range pdbList.Items {
			if err := pdbClient.Delete(ctx, pdb.Name, metav1.DeleteOptions{}); err != nil {
				log.Warnf("failed to delete poddisruptionbudget %s: %v", pdb.Name, err)
			} else {
				resourcesDeleted = append(resourcesDeleted, "poddisruptionbudget:"+pdb.Name)
			}
		}
	}

	// 4. Delete Deployments
	depClient := d.clientset.AppsV1().Deployments(namespace)
	depList, err := depClient.List(ctx, listOptions)
	if err != nil {
		log.Warnf("failed to list deployments for %s: %v", externalID, err)
	} else {
		for _, dep := range depList.Items {
			if err := depClient.Delete(ctx, dep.Name, metav1.DeleteOptions{}); err != nil {
				log.Warnf("failed to delete deployment %s: %v", dep.Name, err)
			} else {
				resourcesDeleted = append(resourcesDeleted, "deployment:"+dep.Name)
			}
		}
	}

	// 5. Delete PersistentVolumeClaims
	pvcClient := d.clientset.CoreV1().PersistentVolumeClaims(namespace)
	pvcList, err := pvcClient.List(ctx, listOptions)
	if err != nil {
		log.Warnf("failed to list persistentvolumeclaims for %s: %v", externalID, err)
	} else {
		for _, pvc := range pvcList.Items {
			if err := pvcClient.Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
				log.Warnf("failed to delete persistentvolumeclaim %s: %v", pvc.Name, err)
			} else {
				resourcesDeleted = append(resourcesDeleted, "persistentvolumeclaim:"+pvc.Name)
			}
		}
	}

	// 6. Delete PersistentVolumes (with Retain policy, need manual deletion)
	pvClient := d.clientset.CoreV1().PersistentVolumes()
	pvList, err := pvClient.List(ctx, listOptions)
	if err != nil {
		log.Warnf("failed to list persistentvolumes for %s: %v", externalID, err)
	} else {
		for _, pv := range pvList.Items {
			if err := pvClient.Delete(ctx, pv.Name, metav1.DeleteOptions{}); err != nil {
				log.Warnf("failed to delete persistentvolume %s: %v", pv.Name, err)
			} else {
				resourcesDeleted = append(resourcesDeleted, "persistentvolume:"+pv.Name)
			}
		}
	}

	// 7. Delete ConfigMaps
	cmClient := d.clientset.CoreV1().ConfigMaps(namespace)
	cmList, err := cmClient.List(ctx, listOptions)
	if err != nil {
		log.Warnf("failed to list configmaps for %s: %v", externalID, err)
	} else {
		for _, cm := range cmList.Items {
			if err := cmClient.Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil {
				log.Warnf("failed to delete configmap %s: %v", cm.Name, err)
			} else {
				resourcesDeleted = append(resourcesDeleted, "configmap:"+cm.Name)
			}
		}
	}

	log.Infof("successfully deleted deployment for external-id %s, deleted %d resources", externalID, len(resourcesDeleted))

	return &vicetypes.DeploymentResponse{
		Status:           "deleted",
		ExternalID:       externalID,
		ResourcesDeleted: resourcesDeleted,
	}, nil
}

// GetStatus returns the current status of a deployment.
func (d *Deployer) GetStatus(ctx context.Context, externalID string, namespace string) (*vicetypes.DeploymentStatus, error) {
	if namespace == "" {
		namespace = d.namespace
	}

	labelSelector := labels.Set(map[string]string{
		"external-id": externalID,
	}).AsSelector().String()

	listOptions := metav1.ListOptions{
		LabelSelector: labelSelector,
	}

	status := &vicetypes.DeploymentStatus{
		ExternalID: externalID,
		Exists:     false,
	}

	// Check Deployment
	depClient := d.clientset.AppsV1().Deployments(namespace)
	depList, err := depClient.List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}

	if len(depList.Items) > 0 {
		status.Exists = true
		dep := depList.Items[0]
		status.DeploymentStatus = &vicetypes.DeploymentStatusInfo{
			ReadyReplicas:     dep.Status.ReadyReplicas,
			AvailableReplicas: dep.Status.AvailableReplicas,
			Replicas:          dep.Status.Replicas,
		}
	}

	// Check Pods
	podClient := d.clientset.CoreV1().Pods(namespace)
	podList, err := podClient.List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	for _, pod := range podList.Items {
		podStatus := vicetypes.PodStatus{
			Name:              pod.Name,
			Phase:             string(pod.Status.Phase),
			ContainerStatuses: pod.Status.ContainerStatuses,
			Message:           pod.Status.Message,
			Reason:            pod.Status.Reason,
		}
		status.Pods = append(status.Pods, podStatus)
	}

	// Check Service
	svcClient := d.clientset.CoreV1().Services(namespace)
	svcList, err := svcClient.List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}
	status.ServiceExists = len(svcList.Items) > 0

	// Check Ingress
	ingressClient := d.clientset.NetworkingV1().Ingresses(namespace)
	ingressList, err := ingressClient.List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list ingresses: %w", err)
	}
	status.IngressExists = len(ingressList.Items) > 0

	return status, nil
}

// CheckURLReady checks if the deployment is ready to serve traffic.
func (d *Deployer) CheckURLReady(ctx context.Context, externalID string, namespace string) (*vicetypes.URLReadyResponse, error) {
	status, err := d.GetStatus(ctx, externalID, namespace)
	if err != nil {
		return nil, err
	}

	podReady := false
	for _, pod := range status.Pods {
		if pod.Phase == "Running" {
			// Check if all containers are ready
			allReady := true
			for _, cs := range pod.ContainerStatuses {
				if !cs.Ready {
					allReady = false
					break
				}
			}
			if allReady {
				podReady = true
				break
			}
		}
	}

	return &vicetypes.URLReadyResponse{
		Ready:         status.IngressExists && status.ServiceExists && podReady,
		IngressExists: status.IngressExists,
		ServiceExists: status.ServiceExists,
		PodReady:      podReady,
	}, nil
}

// GetLogs retrieves logs from pods associated with the deployment.
func (d *Deployer) GetLogs(ctx context.Context, externalID string, namespace string, req *vicetypes.LogsRequest) (*vicetypes.LogsResponse, error) {
	if namespace == "" {
		namespace = d.namespace
	}

	labelSelector := labels.Set(map[string]string{
		"external-id": externalID,
	}).AsSelector().String()

	listOptions := metav1.ListOptions{
		LabelSelector: labelSelector,
	}

	podClient := d.clientset.CoreV1().Pods(namespace)
	podList, err := podClient.List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return &vicetypes.LogsResponse{
			Error: "no pods found for deployment",
		}, nil
	}

	// Get logs from the first pod
	pod := podList.Items[0]
	logOptions := &apiv1.PodLogOptions{
		Container:    req.Container,
		SinceSeconds: req.SinceSeconds,
		TailLines:    req.TailLines,
		Previous:     req.Previous,
	}

	logReq := podClient.GetLogs(pod.Name, logOptions)
	logStream, err := logReq.Stream(ctx)
	if err != nil {
		return &vicetypes.LogsResponse{
			Error: fmt.Sprintf("failed to get log stream: %v", err),
		}, nil
	}
	defer logStream.Close()

	// Read logs
	buf := make([]byte, 1024*1024) // 1MB buffer
	n, err := logStream.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return &vicetypes.LogsResponse{
			Error: fmt.Sprintf("failed to read logs: %v", err),
		}, nil
	}

	return &vicetypes.LogsResponse{
		Logs: string(buf[:n]),
	}, nil
}

// Health returns the health status of the deployer.
func (d *Deployer) Health(ctx context.Context) *vicetypes.HealthResponse {
	// Check Kubernetes connectivity
	_, err := d.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	k8sHealthy := err == nil

	status := "healthy"
	message := ""
	if !k8sHealthy {
		status = "unhealthy"
		message = fmt.Sprintf("kubernetes API unreachable: %v", err)
	}

	return &vicetypes.HealthResponse{
		Status:     status,
		Version:    Version,
		Kubernetes: k8sHealthy,
		Message:    message,
	}
}

// TriggerFileTransfer initiates a file transfer (upload or download) on a deployment.
func (d *Deployer) TriggerFileTransfer(ctx context.Context, externalID, namespace, transferType string, async bool) (*vicetypes.FileTransferResponse, error) {
	if namespace == "" {
		namespace = d.namespace
	}

	log.Infof("triggering %s file transfer for %s", transferType, externalID)

	// Find the service for this deployment
	labelSelector := labels.Set(map[string]string{
		"external-id": externalID,
	}).AsSelector().String()

	listOptions := metav1.ListOptions{
		LabelSelector: labelSelector,
	}

	svcClient := d.clientset.CoreV1().Services(namespace)
	svcList, err := svcClient.List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	if len(svcList.Items) == 0 {
		return &vicetypes.FileTransferResponse{
			Status: "error",
			Error:  "no services found for deployment",
		}, nil
	}

	svc := svcList.Items[0]

	// Determine the request path based on transfer type
	var reqPath string
	switch transferType {
	case "upload":
		reqPath = constants.UploadBasePath
	case "download":
		reqPath = constants.DownloadBasePath
	default:
		return &vicetypes.FileTransferResponse{
			Status: "error",
			Error:  fmt.Sprintf("invalid transfer type: %s", transferType),
		}, nil
	}

	// Call the file transfer service
	xferResp, err := d.requestTransfer(ctx, svc, reqPath)
	if err != nil {
		return &vicetypes.FileTransferResponse{
			Status: "error",
			Error:  fmt.Sprintf("failed to initiate transfer: %v", err),
		}, nil
	}

	// If async, return immediately
	if async {
		return &vicetypes.FileTransferResponse{
			Status:     "in_progress",
			TransferID: xferResp.UUID,
			Message:    fmt.Sprintf("%s transfer initiated", transferType),
		}, nil
	}

	// Wait for transfer to complete
	return d.waitForTransfer(ctx, svc, reqPath, xferResp.UUID, transferType)
}

type transferResponse struct {
	UUID   string `json:"uuid"`
	Status string `json:"status"`
	Kind   string `json:"kind"`
}

func (d *Deployer) requestTransfer(ctx context.Context, svc apiv1.Service, reqPath string) (*transferResponse, error) {
	svcURL := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s:%d", svc.Name, svc.Namespace, constants.FileTransfersPort),
		Path:   reqPath,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, svcURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 399 {
		return nil, fmt.Errorf("transfer request returned status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var xferResp transferResponse
	if err := json.Unmarshal(bodyBytes, &xferResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &xferResp, nil
}

func (d *Deployer) getTransferStatus(ctx context.Context, svc apiv1.Service, reqPath, uuid string) (*transferResponse, error) {
	svcURL := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s:%d", svc.Name, svc.Namespace, constants.FileTransfersPort),
		Path:   path.Join(reqPath, uuid),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, svcURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 399 {
		return nil, fmt.Errorf("status request returned status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var xferResp transferResponse
	if err := json.Unmarshal(bodyBytes, &xferResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &xferResp, nil
}

func (d *Deployer) waitForTransfer(ctx context.Context, svc apiv1.Service, reqPath, uuid, transferType string) (*vicetypes.FileTransferResponse, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return &vicetypes.FileTransferResponse{
				Status:     "error",
				TransferID: uuid,
				Error:      "context cancelled",
			}, ctx.Err()
		case <-ticker.C:
			xferResp, err := d.getTransferStatus(ctx, svc, reqPath, uuid)
			if err != nil {
				log.Warnf("error getting transfer status: %v", err)
				continue
			}

			switch xferResp.Status {
			case "completed":
				return &vicetypes.FileTransferResponse{
					Status:     "completed",
					TransferID: uuid,
					Message:    fmt.Sprintf("%s completed successfully", transferType),
				}, nil
			case "failed":
				return &vicetypes.FileTransferResponse{
					Status:     "failed",
					TransferID: uuid,
					Error:      fmt.Sprintf("%s failed", transferType),
				}, nil
			default:
				// Still in progress
				log.Debugf("transfer %s status: %s", uuid, xferResp.Status)
			}
		}
	}
}

// upsertConfigMap creates or updates a ConfigMap.
func (d *Deployer) upsertConfigMap(ctx context.Context, namespace string, cm *apiv1.ConfigMap) error {
	client := d.clientset.CoreV1().ConfigMaps(namespace)
	_, err := client.Get(ctx, cm.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, cm, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, cm, metav1.UpdateOptions{})
	}
	return err
}

// upsertPersistentVolume creates or updates a PersistentVolume.
func (d *Deployer) upsertPersistentVolume(ctx context.Context, pv *apiv1.PersistentVolume) error {
	client := d.clientset.CoreV1().PersistentVolumes()
	_, err := client.Get(ctx, pv.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, pv, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, pv, metav1.UpdateOptions{})
	}
	return err
}

// upsertPersistentVolumeClaim creates or updates a PersistentVolumeClaim.
func (d *Deployer) upsertPersistentVolumeClaim(ctx context.Context, namespace string, pvc *apiv1.PersistentVolumeClaim) error {
	client := d.clientset.CoreV1().PersistentVolumeClaims(namespace)
	_, err := client.Get(ctx, pvc.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, pvc, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, pvc, metav1.UpdateOptions{})
	}
	return err
}

// upsertDeployment creates or updates a Deployment.
func (d *Deployer) upsertDeployment(ctx context.Context, namespace string, dep *appsv1.Deployment) error {
	client := d.clientset.AppsV1().Deployments(namespace)
	_, err := client.Get(ctx, dep.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, dep, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, dep, metav1.UpdateOptions{})
	}
	return err
}

// upsertPodDisruptionBudget creates or updates a PodDisruptionBudget.
func (d *Deployer) upsertPodDisruptionBudget(ctx context.Context, namespace string, pdb *policyv1.PodDisruptionBudget) error {
	client := d.clientset.PolicyV1().PodDisruptionBudgets(namespace)
	_, err := client.Get(ctx, pdb.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, pdb, metav1.CreateOptions{})
	}
	// PDBs cannot be updated, so we only create if it doesn't exist
	return err
}

// upsertService creates or updates a Service.
func (d *Deployer) upsertService(ctx context.Context, namespace string, svc *apiv1.Service) error {
	client := d.clientset.CoreV1().Services(namespace)
	existing, err := client.Get(ctx, svc.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, svc, metav1.CreateOptions{})
	} else {
		// Preserve ClusterIP on update
		svc.Spec.ClusterIP = existing.Spec.ClusterIP
		svc.ResourceVersion = existing.ResourceVersion
		_, err = client.Update(ctx, svc, metav1.UpdateOptions{})
	}
	return err
}

// upsertIngress creates or updates an Ingress.
func (d *Deployer) upsertIngress(ctx context.Context, namespace string, ingress *netv1.Ingress) error {
	client := d.clientset.NetworkingV1().Ingresses(namespace)
	existing, err := client.Get(ctx, ingress.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, ingress, metav1.CreateOptions{})
	} else {
		ingress.ResourceVersion = existing.ResourceVersion
		_, err = client.Update(ctx, ingress, metav1.UpdateOptions{})
	}
	return err
}
