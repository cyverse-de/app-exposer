package operator

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/cyverse-de/app-exposer/operatorclient"
)

// analysisLabelSelector returns a ListOptions that selects resources by analysis-id.
func analysisLabelSelector(analysisID string) metav1.ListOptions {
	set := labels.Set{"analysis-id": analysisID}
	return metav1.ListOptions{LabelSelector: set.AsSelector().String()}
}

// k8sResource abstracts the Get/Create/Update operations shared by all K8s
// resource clients, allowing a single generic upsert implementation.
type k8sResource[T any] interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (T, error)
	Create(ctx context.Context, obj T, opts metav1.CreateOptions) (T, error)
	Update(ctx context.Context, obj T, opts metav1.UpdateOptions) (T, error)
}

// upsert creates or updates a K8s resource. If the resource doesn't exist it
// is created; otherwise it is updated. On update, the ResourceVersion from the
// existing object is copied to obj for optimistic concurrency control (the K8s
// API server requires it). The kind string is used only for logging.
func upsert[T metav1.Object](ctx context.Context, client k8sResource[T], kind, name string, obj T) error {
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating %s %s", kind, name)
		if _, err = client.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating %s %s: %w", kind, name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing %s %s: %w", kind, name, err)
	}
	// Copy ResourceVersion for optimistic concurrency — the API server
	// requires it on updates to detect conflicts.
	obj.SetResourceVersion(existing.GetResourceVersion())
	log.Debugf("updating %s %s", kind, name)
	if _, err = client.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating %s %s: %w", kind, name, err)
	}
	return nil
}

// applyBundle creates or updates all K8s resources in the bundle.
func (o *Operator) applyBundle(ctx context.Context, bundle *operatorclient.AnalysisBundle) error {
	log.Infof("applying bundle for analysis %s (%d configmaps, %d pvs, %d pvcs)",
		bundle.AnalysisID, len(bundle.ConfigMaps), len(bundle.PersistentVolumes), len(bundle.PersistentVolumeClaims))

	for _, cm := range bundle.ConfigMaps {
		if cm == nil {
			continue
		}
		if err := upsert(ctx, o.clientset.CoreV1().ConfigMaps(o.namespace), "ConfigMap", cm.Name, cm); err != nil {
			return fmt.Errorf("configmap %s: %w", cm.Name, err)
		}
	}

	for _, pv := range bundle.PersistentVolumes {
		if pv == nil {
			continue
		}
		if err := upsert(ctx, o.clientset.CoreV1().PersistentVolumes(), "PersistentVolume", pv.Name, pv); err != nil {
			return fmt.Errorf("pv %s: %w", pv.Name, err)
		}
	}

	for _, pvc := range bundle.PersistentVolumeClaims {
		if pvc == nil {
			continue
		}
		if err := upsert(ctx, o.clientset.CoreV1().PersistentVolumeClaims(o.namespace), "PersistentVolumeClaim", pvc.Name, pvc); err != nil {
			return fmt.Errorf("pvc %s: %w", pvc.Name, err)
		}
	}

	if bundle.Deployment != nil {
		if err := upsert(ctx, o.clientset.AppsV1().Deployments(o.namespace), "Deployment", bundle.Deployment.Name, bundle.Deployment); err != nil {
			return fmt.Errorf("deployment %s: %w", bundle.Deployment.Name, err)
		}
	}

	if bundle.Service != nil {
		if err := upsert(ctx, o.clientset.CoreV1().Services(o.namespace), "Service", bundle.Service.Name, bundle.Service); err != nil {
			return fmt.Errorf("service %s: %w", bundle.Service.Name, err)
		}
	}

	if bundle.HTTPRoute != nil {
		if err := upsert(ctx, o.gatewayClient.HTTPRoutes(o.namespace), "HTTPRoute", bundle.HTTPRoute.Name, bundle.HTTPRoute); err != nil {
			return fmt.Errorf("httproute %s: %w", bundle.HTTPRoute.Name, err)
		}
	}

	if bundle.PodDisruptionBudget != nil {
		if err := upsert(ctx, o.clientset.PolicyV1().PodDisruptionBudgets(o.namespace), "PodDisruptionBudget", bundle.PodDisruptionBudget.Name, bundle.PodDisruptionBudget); err != nil {
			return fmt.Errorf("pdb %s: %w", bundle.PodDisruptionBudget.Name, err)
		}
	}

	log.Infof("bundle applied for analysis %s", bundle.AnalysisID)
	return nil
}

// deleteAnalysisResources deletes all K8s resources matching the analysis-id label.
// All resource types are attempted even if earlier deletions fail, and all errors
// are collected and returned together. 404 errors on individual deletes are ignored
// since the operation is idempotent.
func (o *Operator) deleteAnalysisResources(ctx context.Context, analysisID string) error {
	log.Infof("deleting resources for analysis %s", analysisID)
	opts := analysisLabelSelector(analysisID)
	var errs []error

	// deleteItem calls deleteFn with the given name and accumulates non-404 errors.
	deleteItem := func(name string, deleteFn func(string) error) {
		if err := deleteFn(name); err != nil && !apierrors.IsNotFound(err) {
			log.Errorf("deleting %s for analysis %s: %v", name, analysisID, err)
			errs = append(errs, err)
		}
	}

	// Delete PodDisruptionBudgets
	pdbClient := o.clientset.PolicyV1().PodDisruptionBudgets(o.namespace)
	if pdbList, err := pdbClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing PDBs: %w", err))
	} else {
		for _, pdb := range pdbList.Items {
			deleteItem(pdb.Name, func(name string) error {
				return pdbClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	// Delete HTTPRoutes
	routeClient := o.gatewayClient.HTTPRoutes(o.namespace)
	if routeList, err := routeClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing HTTPRoutes: %w", err))
	} else {
		for _, route := range routeList.Items {
			deleteItem(route.Name, func(name string) error {
				return routeClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	// Delete Services
	svcClient := o.clientset.CoreV1().Services(o.namespace)
	if svcList, err := svcClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing Services: %w", err))
	} else {
		for _, svc := range svcList.Items {
			deleteItem(svc.Name, func(name string) error {
				return svcClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	// Delete Deployments
	depClient := o.clientset.AppsV1().Deployments(o.namespace)
	if depList, err := depClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing Deployments: %w", err))
	} else {
		for _, dep := range depList.Items {
			deleteItem(dep.Name, func(name string) error {
				return depClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	// Delete PVCs — also triggers automatic PV cleanup for Delete reclaim policy volumes.
	pvcClient := o.clientset.CoreV1().PersistentVolumeClaims(o.namespace)
	if pvcList, err := pvcClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing PVCs: %w", err))
	} else {
		for _, pvc := range pvcList.Items {
			deleteItem(pvc.Name, func(name string) error {
				return pvcClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	// Delete PVs with Retain reclaim policy (CSI driver volumes not cleaned up via PVC deletion).
	pvClient := o.clientset.CoreV1().PersistentVolumes()
	if pvList, err := pvClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing PVs: %w", err))
	} else {
		for _, pv := range pvList.Items {
			deleteItem(pv.Name, func(name string) error {
				return pvClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	// Delete ConfigMaps
	cmClient := o.clientset.CoreV1().ConfigMaps(o.namespace)
	if cmList, err := cmClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing ConfigMaps: %w", err))
	} else {
		for _, cm := range cmList.Items {
			deleteItem(cm.Name, func(name string) error {
				return cmClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	// Delete per-analysis NetworkPolicies (egress allow policies).
	npClient := o.clientset.NetworkingV1().NetworkPolicies(o.namespace)
	if npList, err := npClient.List(ctx, opts); err != nil {
		errs = append(errs, fmt.Errorf("listing NetworkPolicies: %w", err))
	} else {
		for _, np := range npList.Items {
			deleteItem(np.Name, func(name string) error {
				return npClient.Delete(ctx, name, metav1.DeleteOptions{})
			})
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to delete resources for analysis %s: %w", analysisID, errors.Join(errs...))
	}

	log.Infof("resources deleted for analysis %s", analysisID)
	return nil
}
