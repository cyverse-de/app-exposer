package operator

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/cyverse-de/app-exposer/operatorclient"
)

// analysisLabelSelector returns a ListOptions that selects resources by analysis-id.
func analysisLabelSelector(analysisID string) metav1.ListOptions {
	set := labels.Set{"analysis-id": analysisID}
	return metav1.ListOptions{LabelSelector: set.AsSelector().String()}
}

// applyBundle creates or updates all K8s resources in the bundle.
func (o *Operator) applyBundle(ctx context.Context, bundle *operatorclient.AnalysisBundle) error {
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

	// Ingress
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

	return nil
}

func (o *Operator) upsertConfigMap(ctx context.Context, cm *apiv1.ConfigMap) error {
	client := o.clientset.CoreV1().ConfigMaps(o.namespace)
	_, err := client.Get(ctx, cm.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, cm, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, cm, metav1.UpdateOptions{})
	}
	return err
}

func (o *Operator) upsertPersistentVolume(ctx context.Context, pv *apiv1.PersistentVolume) error {
	client := o.clientset.CoreV1().PersistentVolumes()
	_, err := client.Get(ctx, pv.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, pv, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, pv, metav1.UpdateOptions{})
	}
	return err
}

func (o *Operator) upsertPersistentVolumeClaim(ctx context.Context, pvc *apiv1.PersistentVolumeClaim) error {
	client := o.clientset.CoreV1().PersistentVolumeClaims(o.namespace)
	_, err := client.Get(ctx, pvc.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, pvc, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, pvc, metav1.UpdateOptions{})
	}
	return err
}

func (o *Operator) upsertDeployment(ctx context.Context, dep *appsv1.Deployment) error {
	client := o.clientset.AppsV1().Deployments(o.namespace)
	_, err := client.Get(ctx, dep.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, dep, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, dep, metav1.UpdateOptions{})
	}
	return err
}

func (o *Operator) upsertService(ctx context.Context, svc *apiv1.Service) error {
	client := o.clientset.CoreV1().Services(o.namespace)
	_, err := client.Get(ctx, svc.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, svc, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, svc, metav1.UpdateOptions{})
	}
	return err
}

func (o *Operator) upsertIngress(ctx context.Context, ing *netv1.Ingress) error {
	client := o.clientset.NetworkingV1().Ingresses(o.namespace)
	_, err := client.Get(ctx, ing.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, ing, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, ing, metav1.UpdateOptions{})
	}
	return err
}

func (o *Operator) upsertPDB(ctx context.Context, pdb *policyv1.PodDisruptionBudget) error {
	client := o.clientset.PolicyV1().PodDisruptionBudgets(o.namespace)
	_, err := client.Get(ctx, pdb.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.Create(ctx, pdb, metav1.CreateOptions{})
	} else {
		_, err = client.Update(ctx, pdb, metav1.UpdateOptions{})
	}
	return err
}

// deleteAnalysisResources deletes all K8s resources matching the analysis-id label.
func (o *Operator) deleteAnalysisResources(ctx context.Context, analysisID string) error {
	opts := analysisLabelSelector(analysisID)

	// Delete PDB
	pdbClient := o.clientset.PolicyV1().PodDisruptionBudgets(o.namespace)
	pdbList, err := pdbClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, pdb := range pdbList.Items {
		if err := pdbClient.Delete(ctx, pdb.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
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
		}
	}

	// Delete PVCs (also triggers PV cleanup for bound volumes)
	pvcClient := o.clientset.CoreV1().PersistentVolumeClaims(o.namespace)
	pvcList, err := pvcClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, pvc := range pvcList.Items {
		if err := pvcClient.Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
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
		}
	}

	return nil
}
