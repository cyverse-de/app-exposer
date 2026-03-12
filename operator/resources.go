package operator

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
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

	// HTTPRoute (gateway-first)
	if bundle.HTTPRoute != nil && o.hasGatewayClient() {
		if err := o.upsertHTTPRoute(ctx, bundle.HTTPRoute); err != nil {
			return fmt.Errorf("httproute %s: %w", bundle.HTTPRoute.Name, err)
		}
	}

	// Ingress (fallback or legacy)
	if bundle.Ingress != nil {
		if err := o.upsertIngress(ctx, bundle.Ingress); err != nil {
			return fmt.Errorf("ingress %s: %w", bundle.Ingress.Name, err)
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

func (o *Operator) upsertIngress(ctx context.Context, ing *netv1.Ingress) error {
	client := o.clientset.NetworkingV1().Ingresses(o.namespace)
	_, err := client.Get(ctx, ing.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Debugf("creating Ingress %s", ing.Name)
		_, err = client.Create(ctx, ing, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("checking for existing Ingress %s: %w", ing.Name, err)
	}
	log.Debugf("updating Ingress %s", ing.Name)
	_, err = client.Update(ctx, ing, metav1.UpdateOptions{})
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
func (o *Operator) deleteAnalysisResources(ctx context.Context, analysisID string) error {
	log.Infof("deleting resources for analysis %s", analysisID)
	opts := analysisLabelSelector(analysisID)
	var errs []error

	// Delete PDB
	pdbClient := o.clientset.PolicyV1().PodDisruptionBudgets(o.namespace)
	pdbList, err := pdbClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, pdb := range pdbList.Items {
		if err := pdbClient.Delete(ctx, pdb.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
			errs = append(errs, err)
		}
	}

	// Delete HTTPRoutes if gateway client is available.
	if o.hasGatewayClient() {
		routeClient := o.gatewayClient.HTTPRoutes(o.namespace)
		routeList, err := routeClient.List(ctx, opts)
		if err != nil {
			log.Errorf("error listing HTTPRoutes for deletion: %v", err)
		} else {
			for _, route := range routeList.Items {
				if err := routeClient.Delete(ctx, route.Name, metav1.DeleteOptions{}); err != nil {
					log.Error(err)
					errs = append(errs, err)
				}
			}
		}
	}

	// Delete Ingress
	ingClient := o.clientset.NetworkingV1().Ingresses(o.namespace)
	ingList, err := ingClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, ing := range ingList.Items {
		if err := ingClient.Delete(ctx, ing.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
			errs = append(errs, err)
		}
	}

	// Delete Service
	svcClient := o.clientset.CoreV1().Services(o.namespace)
	svcList, err := svcClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, svc := range svcList.Items {
		if err := svcClient.Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
			errs = append(errs, err)
		}
	}

	// Delete Deployment
	depClient := o.clientset.AppsV1().Deployments(o.namespace)
	depList, err := depClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, dep := range depList.Items {
		if err := depClient.Delete(ctx, dep.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
			errs = append(errs, err)
		}
	}

	// Delete PVCs (triggers automatic PV cleanup for volumes with Delete reclaim policy).
	pvcClient := o.clientset.CoreV1().PersistentVolumeClaims(o.namespace)
	pvcList, err := pvcClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, pvc := range pvcList.Items {
		if err := pvcClient.Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
			errs = append(errs, err)
		}
	}

	// Delete PVs with "Retain" reclaim policy (CSI driver volumes)
	pvClient := o.clientset.CoreV1().PersistentVolumes()
	pvList, err := pvClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, pv := range pvList.Items {
		if err := pvClient.Delete(ctx, pv.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
			errs = append(errs, err)
		}
	}

	// Delete ConfigMaps
	cmClient := o.clientset.CoreV1().ConfigMaps(o.namespace)
	cmList, err := cmClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, cm := range cmList.Items {
		if err := cmClient.Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to delete %d resources for analysis %s: %w", len(errs), analysisID, errors.Join(errs...))
	}

	log.Infof("resources deleted for analysis %s", analysisID)
	return nil
}
