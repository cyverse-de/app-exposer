package incluster

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/cockroachdb/apd"
	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/quota"

	"github.com/jmoiron/sqlx"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/cyverse-de/model/v9"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

var log = common.Log
var httpClient = http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
var otelName = "github.com/cyverse-de/app-exposer/incluster"

// Init contains configuration for configuring an *Incluster.
type Init struct {
	PorklockImage                 string
	PorklockTag                   string
	UseCSIDriver                  bool
	InputPathListIdentifier       string
	TicketInputPathListIdentifier string
	ImagePullSecretName           string
	ViceProxyImage                string
	FrontendBaseURL               string
	ViceDefaultBackendService     string
	ViceDefaultBackendServicePort int
	GetAnalysisIDService          string
	CheckResourceAccessService    string
	VICEBackendNamespace          string
	AppsServiceBaseURL            string
	ViceNamespace                 string
	JobStatusURL                  string
	UserSuffix                    string
	PermissionsURL                string
	KeycloakBaseURL               string
	KeycloakRealm                 string
	KeycloakClientID              string
	KeycloakClientSecret          string
	IRODSZone                     string
	IngressClass                  string
	LocalStorageClass             string
	DisableViceProxyAuth          bool
	NATSEncodedConn               *nats.EncodedConn
	BypassUsers                   []string
	TimeLimitExtensionSeconds     int64
}

// Incluster contains information and operations for launching VICE apps inside the
// local k8s cluster.
type Incluster struct {
	Init
	clientset       kubernetes.Interface
	db              *sqlx.DB
	statusPublisher AnalysisStatusPublisher
	apps            *apps.Apps
	quotaEnforcer   *quota.Enforcer
}

// New creates a new *Incluster.
func New(init *Init, db *sqlx.DB, clientset kubernetes.Interface, apps *apps.Apps) *Incluster {
	return &Incluster{
		Init:      *init,
		db:        db,
		clientset: clientset,
		statusPublisher: &JSLPublisher{
			statusURL: init.JobStatusURL,
		},
		apps:          apps,
		quotaEnforcer: quota.NewEnforcer(clientset, db, apps, init.NATSEncodedConn, init.UserSuffix),
	}
}

// LabelsFromJob returns a map[string]string that can be used as labels for K8s resources.
func (i *Incluster) LabelsFromJob(ctx context.Context, job *model.Job) (map[string]string, error) {
	name := []rune(job.Name)

	var stringmax int
	if len(name) >= 63 {
		stringmax = 62
	} else {
		stringmax = len(name) - 1
	}

	ipAddr, err := i.apps.GetUserIP(ctx, job.UserID)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"external-id":   job.InvocationID,
		"app-name":      common.LabelValueString(job.AppName),
		"app-id":        job.AppID,
		"username":      common.LabelValueString(job.Submitter),
		"user-id":       job.UserID,
		"analysis-name": common.LabelValueString(string(name[:stringmax])),
		"app-type":      "interactive",
		"subdomain":     IngressName(job.UserID, job.InvocationID),
		"login-ip":      ipAddr,
	}, nil
}

// UpsertExcludesConfigMap uses the Job passed in to assemble the ConfigMap
// containing the files that should not be uploaded to iRODS. It then calls
// the k8s API to create the ConfigMap if it does not already exist or to
// update it if it does.
func (i *Incluster) UpsertExcludesConfigMap(ctx context.Context, job *model.Job) error {
	excludesCM, err := i.excludesConfigMap(ctx, job)
	if err != nil {
		return err
	}

	cmclient := i.clientset.CoreV1().ConfigMaps(i.ViceNamespace)

	_, err = cmclient.Get(ctx, excludesConfigMapName(job), metav1.GetOptions{})
	if err != nil {
		log.Info(err)
		_, err = cmclient.Create(ctx, excludesCM, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		_, err = cmclient.Update(ctx, excludesCM, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// UpsertInputPathListConfigMap uses the Job passed in to assemble the ConfigMap
// containing the path list of files to download from iRODS for the VICE analysis.
// It then uses the k8s API to create the ConfigMap if it does not already exist or to
// update it if it does.
func (i *Incluster) UpsertInputPathListConfigMap(ctx context.Context, job *model.Job) error {
	inputCM, err := i.inputPathListConfigMap(ctx, job)
	if err != nil {
		return err
	}

	cmclient := i.clientset.CoreV1().ConfigMaps(i.ViceNamespace)

	_, err = cmclient.Get(ctx, inputPathListConfigMapName(job), metav1.GetOptions{})
	if err != nil {
		_, err = cmclient.Create(ctx, inputCM, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		_, err = cmclient.Update(ctx, inputCM, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

// UpsertDeployment uses the Job passed in to assemble a Deployment for the
// VICE analysis. If then uses the k8s API to create the Deployment if it does
// not already exist or to update it if it does.
func (i *Incluster) UpsertDeployment(ctx context.Context, deployment *appsv1.Deployment, job *model.Job) error {
	var err error
	depclient := i.clientset.AppsV1().Deployments(i.ViceNamespace)

	_, err = depclient.Get(ctx, job.InvocationID, metav1.GetOptions{})
	if err != nil {
		_, err = depclient.Create(ctx, deployment, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		_, err = depclient.Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	persistentVolumes, err := i.getPersistentVolumes(ctx, job)
	if err != nil {
		return err
	}

	volumeClaims, err := i.getVolumeClaims(ctx, job)
	if err != nil {
		return err
	}

	if len(persistentVolumes) > 0 {
		pvclient := i.clientset.CoreV1().PersistentVolumes()

		for _, volume := range persistentVolumes {
			_, err = pvclient.Get(ctx, volume.GetName(), metav1.GetOptions{})
			if err != nil {
				_, err = pvclient.Create(ctx, volume, metav1.CreateOptions{})
				if err != nil {
					return err
				}
			} else {
				_, err = pvclient.Update(ctx, volume, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
			}
		}
	}

	if len(volumeClaims) > 0 {
		pvcclient := i.clientset.CoreV1().PersistentVolumeClaims(i.ViceNamespace)

		for _, volumeClaim := range volumeClaims {
			_, err = pvcclient.Get(ctx, volumeClaim.GetName(), metav1.GetOptions{})
			if err != nil {
				_, err = pvcclient.Create(ctx, volumeClaim, metav1.CreateOptions{})
				if err != nil {
					return err
				}
			} else {
				_, err = pvcclient.Update(ctx, volumeClaim, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
			}
		}
	}

	// Create the pod disruption budget for the job.
	pdb, err := i.createPodDisruptionBudget(ctx, job)
	if err != nil {
		return err
	}
	pdbClient := i.clientset.PolicyV1().PodDisruptionBudgets(i.ViceNamespace)
	_, err = pdbClient.Get(ctx, job.InvocationID, metav1.GetOptions{})
	if err != nil {
		_, err = pdbClient.Create(ctx, pdb, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	// Create the service for the job.
	svc, err := i.getService(ctx, job)
	if err != nil {
		return err
	}
	svcclient := i.clientset.CoreV1().Services(i.ViceNamespace)
	_, err = svcclient.Get(ctx, job.InvocationID, metav1.GetOptions{})
	if err != nil {
		_, err = svcclient.Create(ctx, svc, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	// Create the ingress for the job
	ingress, err := i.getIngress(ctx, job, svc, i.IngressClass)
	if err != nil {
		return err
	}

	ingressclient := i.clientset.NetworkingV1().Ingresses(i.ViceNamespace)
	_, err = ingressclient.Get(ctx, ingress.Name, metav1.GetOptions{})
	if err != nil {
		_, err = ingressclient.Create(ctx, ingress, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func GetMillicoresFromDeployment(deployment *appsv1.Deployment) (*apd.Decimal, error) {
	var (
		analysisContainer *apiv1.Container
		millicores        *apd.Decimal
		millicoresString  string
		err               error
	)
	containers := deployment.Spec.Template.Spec.Containers

	found := false

	for _, container := range containers {
		if container.Name == constants.AnalysisContainerName {
			analysisContainer = &container
			found = true
			break
		}
	}

	if !found {
		return nil, errors.New("could not find the analysis container in the deployment")
	}

	millicoresString = analysisContainer.Resources.Limits[apiv1.ResourceCPU].ToUnstructured().(string)

	millicores, _, err = apd.NewFromString(millicoresString)
	if err != nil {
		return nil, err
	}

	millicoresPerCPU := apd.New(1000, 0)

	_, err = apd.BaseContext.Mul(millicores, millicores, millicoresPerCPU)
	if err != nil {
		return nil, err
	}

	log.Debugf("%s millicores reservation found", millicores.String())

	return millicores, nil
}

func (i *Incluster) DoExit(ctx context.Context, externalID string) error {
	set := labels.Set(map[string]string{
		"external-id": externalID,
	})

	listoptions := metav1.ListOptions{
		LabelSelector: set.AsSelector().String(),
	}

	// Delete the pod disruption budget
	pdbClient := i.clientset.PolicyV1().PodDisruptionBudgets(i.ViceNamespace)
	pdbList, err := pdbClient.List(ctx, listoptions)
	if err != nil {
		return err
	}

	for _, pdb := range pdbList.Items {
		if err = pdbClient.Delete(ctx, pdb.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}

	// Delete the ingress
	ingressclient := i.clientset.NetworkingV1().Ingresses(i.ViceNamespace)
	ingresslist, err := ingressclient.List(ctx, listoptions)
	if err != nil {
		return err
	}

	for _, ingress := range ingresslist.Items {
		if err = ingressclient.Delete(ctx, ingress.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}

	// Delete the service
	svcclient := i.clientset.CoreV1().Services(i.ViceNamespace)
	svclist, err := svcclient.List(ctx, listoptions)
	if err != nil {
		return err
	}

	for _, svc := range svclist.Items {
		if err = svcclient.Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}

	// Delete the deployment
	depclient := i.clientset.AppsV1().Deployments(i.ViceNamespace)
	deplist, err := depclient.List(ctx, listoptions)
	if err != nil {
		return err
	}

	for _, dep := range deplist.Items {
		if err = depclient.Delete(ctx, dep.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}

	// Delete volumes used by the deployment
	// Delete persistent volume claims.
	// This will automatically delete persistent volumes associated with them.
	pvcclient := i.clientset.CoreV1().PersistentVolumeClaims(i.ViceNamespace)
	pvclist, err := pvcclient.List(ctx, listoptions)
	if err != nil {
		return err
	}

	for _, pvc := range pvclist.Items {
		if err = pvcclient.Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}

	// Persistent volumes with "Retain" reclaim policy should be deleted manually
	// Persistent volumes created via CSI Driver only supports "Retain" reclaim policy
	pvclient := i.clientset.CoreV1().PersistentVolumes()
	pvlist, err := pvclient.List(ctx, listoptions)
	if err != nil {
		return err
	}

	for _, pv := range pvlist.Items {
		if err = pvclient.Delete(ctx, pv.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}

	// Delete the input files list and the excludes list config maps
	cmclient := i.clientset.CoreV1().ConfigMaps(i.ViceNamespace)
	cmlist, err := cmclient.List(ctx, listoptions)
	if err != nil {
		return err
	}

	log.Infof("number of configmaps to be deleted for %s: %d", externalID, len(cmlist.Items))

	for _, cm := range cmlist.Items {
		log.Infof("deleting configmap %s for %s", cm.Name, externalID)
		if err = cmclient.Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}

	return nil
}

// GetIDFromHost returns the external ID for the running VICE app, which
// is assumed to be the same as the name of the ingress.
func (i *Incluster) GetIDFromHost(ctx context.Context, host string) (string, error) {
	ingressclient := i.clientset.NetworkingV1().Ingresses(i.ViceNamespace)
	ingresslist, err := ingressclient.List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	for _, ingress := range ingresslist.Items {
		for _, rule := range ingress.Spec.Rules {
			if rule.Host == host {
				return ingress.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no ingress found for host %s", host)
}

const updateTimeLimitSQL = `
	UPDATE ONLY jobs
	   SET planned_end_date = old_value.planned_end_date + interval '1 second' * $3
	  FROM (SELECT planned_end_date FROM jobs WHERE id = $2) AS old_value
	 WHERE jobs.id = $2
	   AND jobs.user_id = $1
 RETURNING EXTRACT(EPOCH FROM
    jobs.planned_end_date AT TIME ZONE current_setting('TimeZone')
 )::bigint
`

const getTimeLimitSQL = `
	SELECT EXTRACT(EPOCH FROM
	    planned_end_date AT TIME ZONE current_setting('TimeZone')
	)::bigint
	  FROM jobs
	 WHERE jobs.id = $2
	   AND jobs.user_id = $1
`

const getUserIDSQL = `
	SELECT users.id
	  FROM users
	 WHERE username = $1
`

type TimeLimit struct {
	TimeLimit string `json:"time_limit"`
}

func (i *Incluster) GetTimeLimit(ctx context.Context, userID, id string) (*TimeLimit, error) {
	var err error

	var epoch sql.NullInt64
	if err = i.db.QueryRowContext(ctx, getTimeLimitSQL, userID, id).Scan(&epoch); err != nil {
		return nil, errors.Wrapf(err, "error retrieving time limit for user %s on analysis %s", userID, id)
	}

	retval := &TimeLimit{}
	if epoch.Valid {
		retval.TimeLimit = fmt.Sprintf("%d", epoch.Int64)
	} else {
		retval.TimeLimit = "null"
	}

	return retval, nil
}

func (i *Incluster) UpdateTimeLimit(ctx context.Context, user, id string) (*TimeLimit, error) {
	var (
		err    error
		userID string
	)

	if !strings.HasSuffix(user, constants.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, constants.UserSuffix)
	}

	if err = i.db.QueryRowContext(ctx, getUserIDSQL, user).Scan(&userID); err != nil {
		return nil, errors.Wrapf(err, "error looking user ID for %s", user)
	}

	var epoch sql.NullInt64
	if err = i.db.QueryRowContext(ctx, updateTimeLimitSQL, userID, id, i.TimeLimitExtensionSeconds).Scan(&epoch); err != nil {
		return nil, errors.Wrapf(err, "error extending time limit for user %s on analysis %s", userID, id)
	}

	retval := &TimeLimit{}
	if epoch.Valid {
		retval.TimeLimit = fmt.Sprintf("%d", epoch.Int64)
	} else {
		return nil, fmt.Errorf("the time limit for analysis %s was null after extension", id)
	}

	return retval, nil
}

// isUserInBypassWhitelist checks if the given username is in the resource tracking bypass whitelist.
func (i *Incluster) isUserInBypassWhitelist(username string) bool {
	normalizedUser := common.FixUsername(username, i.UserSuffix)
	for _, allowedUser := range i.BypassUsers {
		if normalizedUser == allowedUser {
			return true
		}
	}
	return false
}

func (i *Incluster) ValidateJob(ctx context.Context, job *model.Job) (int, error) {
	if i.isUserInBypassWhitelist(job.Submitter) {
		log.Infof("Resource tracking disabled for user %s (in bypass whitelist), skipping validation for job %s", job.Submitter, job.InvocationID)
		return http.StatusOK, nil
	}
	log.Infof("Resource tracking enabled for user %s, validating job %s", job.Submitter, job.InvocationID)
	return i.quotaEnforcer.ValidateJob(ctx, job, i.ViceNamespace)
}
