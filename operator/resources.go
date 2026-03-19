package operator

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/cyverse-de/app-exposer/operatorclient"
)

// analysisLabelSelector returns a ListOptions that selects resources by analysis-id.
func analysisLabelSelector(analysisID string) metav1.ListOptions {
	set := labels.Set{"analysis-id": analysisID}
	return metav1.ListOptions{LabelSelector: set.AsSelector().String()}
}

// applyBundle creates or updates all K8s resources in the bundle.
func (o *Operator) applyBundle(ctx context.Context, bundle *operatorclient.AnalysisBundle) error {
	log.Infof("applying bundle for analysis %s (%d configmaps, %d pvs, %d pvcs)",
		bundle.AnalysisID, len(bundle.ConfigMaps), len(bundle.PersistentVolumes), len(bundle.PersistentVolumeClaims))

	// ConfigMaps
	for _, cm := range bundle.ConfigMaps {
		if cm == nil {
			continue
		}
		if err := o.upsertConfigMap(ctx, cm); err != nil {
			return fmt.Errorf("configmap %s: %w", cm.Name, err)
		}
	}

	// PersistentVolumes (cluster-scoped)
	for _, pv := range bundle.PersistentVolumes {
		if pv == nil {
			continue
		}
		if err := o.upsertPersistentVolume(ctx, pv); err != nil {
			return fmt.Errorf("pv %s: %w", pv.Name, err)
		}
	}

	// PersistentVolumeClaims
	for _, pvc := range bundle.PersistentVolumeClaims {
		if pvc == nil {
			continue
		}
		if err := o.upsertPersistentVolumeClaim(ctx, pvc); err != nil {
			return fmt.Errorf("pvc %s: %w", pvc.Name, err)
		}
	}

	// Deployment
	if bundle.Deployment != nil {
		if err := o.upsertDeployment(ctx, bundle.Deployment); err != nil {
			return fmt.Errorf("deployment %s: %w", bundle.Deployment.Name, err)
		}
	}

	// Service
	if bundle.Service != nil {
		if err := o.upsertService(ctx, bundle.Service); err != nil {
			return fmt.Errorf("service %s: %w", bundle.Service.Name, err)
		}
	}

	// HTTPRoute
	if bundle.HTTPRoute != nil {
		if err := o.upsertHTTPRoute(ctx, bundle.HTTPRoute); err != nil {
			return fmt.Errorf("httproute %s: %w", bundle.HTTPRoute.Name, err)
		}
	}

	// PodDisruptionBudget
	if bundle.PodDisruptionBudget != nil {
		if err := o.upsertPDB(ctx, bundle.PodDisruptionBudget); err != nil {
			return fmt.Errorf("pdb %s: %w", bundle.PodDisruptionBudget.Name, err)
		}
	}

	log.Infof("bundle applied for analysis %s", bundle.AnalysisID)
	return nil
}

func (o *Operator) upsertConfigMap(ctx context.Context, cm *apiv1.ConfigMap) error {
	client := o.clientset.CoreV1().ConfigMaps(o.namespace)
	_, err := client.Get(ctx, cm.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating ConfigMap %s", cm.Name)
		_, err = client.Create(ctx, cm, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing ConfigMap %s: %w", cm.Name, err)
	}
	log.Debugf("updating ConfigMap %s", cm.Name)
	_, err = client.Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func (o *Operator) upsertPersistentVolume(ctx context.Context, pv *apiv1.PersistentVolume) error {
	client := o.clientset.CoreV1().PersistentVolumes()
	_, err := client.Get(ctx, pv.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating PersistentVolume %s", pv.Name)
		_, err = client.Create(ctx, pv, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing PersistentVolume %s: %w", pv.Name, err)
	}
	log.Debugf("updating PersistentVolume %s", pv.Name)
	_, err = client.Update(ctx, pv, metav1.UpdateOptions{})
	return err
}

func (o *Operator) upsertPersistentVolumeClaim(ctx context.Context, pvc *apiv1.PersistentVolumeClaim) error {
	client := o.clientset.CoreV1().PersistentVolumeClaims(o.namespace)
	_, err := client.Get(ctx, pvc.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating PersistentVolumeClaim %s", pvc.Name)
		_, err = client.Create(ctx, pvc, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing PersistentVolumeClaim %s: %w", pvc.Name, err)
	}
	log.Debugf("updating PersistentVolumeClaim %s", pvc.Name)
	_, err = client.Update(ctx, pvc, metav1.UpdateOptions{})
	return err
}

func (o *Operator) upsertDeployment(ctx context.Context, dep *appsv1.Deployment) error {
	client := o.clientset.AppsV1().Deployments(o.namespace)
	_, err := client.Get(ctx, dep.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating Deployment %s", dep.Name)
		_, err = client.Create(ctx, dep, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing Deployment %s: %w", dep.Name, err)
	}
	log.Debugf("updating Deployment %s", dep.Name)
	_, err = client.Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

func (o *Operator) upsertService(ctx context.Context, svc *apiv1.Service) error {
	client := o.clientset.CoreV1().Services(o.namespace)
	_, err := client.Get(ctx, svc.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating Service %s", svc.Name)
		_, err = client.Create(ctx, svc, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing Service %s: %w", svc.Name, err)
	}
	log.Debugf("updating Service %s", svc.Name)
	_, err = client.Update(ctx, svc, metav1.UpdateOptions{})
	return err
}

// upsertHTTPRoute creates or updates a Gateway API HTTPRoute.
func (o *Operator) upsertHTTPRoute(ctx context.Context, route *gatewayv1.HTTPRoute) error {
	client := o.gatewayClient.HTTPRoutes(o.namespace)
	_, err := client.Get(ctx, route.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating HTTPRoute %s", route.Name)
		_, err = client.Create(ctx, route, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing HTTPRoute %s: %w", route.Name, err)
	}
	log.Debugf("updating HTTPRoute %s", route.Name)
	_, err = client.Update(ctx, route, metav1.UpdateOptions{})
	return err
}

func (o *Operator) upsertPDB(ctx context.Context, pdb *policyv1.PodDisruptionBudget) error {
	client := o.clientset.PolicyV1().PodDisruptionBudgets(o.namespace)
	_, err := client.Get(ctx, pdb.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating PodDisruptionBudget %s", pdb.Name)
		_, err = client.Create(ctx, pdb, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing PodDisruptionBudget %s: %w", pdb.Name, err)
	}
	log.Debugf("updating PodDisruptionBudget %s", pdb.Name)
	_, err = client.Update(ctx, pdb, metav1.UpdateOptions{})
	return err
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

	if len(errs) > 0 {
		return fmt.Errorf("failed to delete resources for analysis %s: %w", analysisID, errors.Join(errs...))
	}

	log.Infof("resources deleted for analysis %s", analysisID)
	return nil
}
