package adapter

import (
	"context"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/batch"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/imageinfo"
	"github.com/cyverse-de/app-exposer/millicores"
	"github.com/cyverse-de/app-exposer/quota"
	"github.com/cyverse-de/model/v9"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

var log = common.Log

// JEXAdapter contains the application state for jex-adapter.
type JEXAdapter struct {
	Init
	apps            *apps.Apps
	detector        *millicores.Detector
	imageInfoGetter imageinfo.InfoGetter
	quotaEnforcer   *quota.Enforcer
	clientset       kubernetes.Interface
}

type Init struct {
	FilterFiles            []string
	LogPath                string
	IRODSBase              string
	FileTransferImage      string
	FileTransferWorkingDir string
	FileTransferLogLevel   string
	StatusSenderImage      string
	Namespace              string
	ImagePullSecretName    string
	BatchExitHandlerImage  string
}

// New returns a *JEXAdapter
func New(init *Init, apps *apps.Apps, detector *millicores.Detector, imageInfoGetter imageinfo.InfoGetter, enforcer *quota.Enforcer, clientset kubernetes.Interface) *JEXAdapter {
	return &JEXAdapter{
		Init:            *init,
		quotaEnforcer:   enforcer,
		clientset:       clientset,
		apps:            apps,
		detector:        detector,
		imageInfoGetter: imageInfoGetter,
	}
}

func (j *JEXAdapter) StopWorkflow(ctx context.Context, externalID string) error {
	ctx, client, err := batch.NewWorkflowServiceClient(ctx)
	if err != nil {
		return err
	}

	if _, err = batch.StopWorkflows(ctx, client, j.Namespace, "external-id", externalID); err != nil {
		return err
	}

	return nil
}

func (j *JEXAdapter) LaunchWorkflow(ctx context.Context, analysis *model.Analysis) error {
	log.Debug("validating analysis")
	if status, err := j.quotaEnforcer.ValidateBatchJob(ctx, analysis, j.Namespace); err != nil {
		if validationErr, ok := err.(common.ErrorResponse); ok {
			log.Error(validationErr)
			return validationErr
		}
		log.Error(err)
		return echo.NewHTTPError(status, err.Error())
	}
	log.Debug("done validating analysis")

	log = log.WithFields(logrus.Fields{
		"external_id": analysis.InvocationID,
	})

	log.Debug("finding number of millicores reserved")
	millicoresReserved, err := j.detector.NumberReserved(analysis)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done finding number of millicores reserved")

	log.Infof("storing %s millicores reserved for %s", millicoresReserved.String(), analysis.InvocationID)
	if err = j.apps.SetMillicoresReserved(analysis, millicoresReserved); err != nil {
		log.Error(err)
		return err
	}
	log.Infof("done storing %s millicores reserved for %s", millicoresReserved.String(), analysis.InvocationID)

	opts := &batch.BatchSubmissionOpts{
		FileTransferImage:      j.FileTransferImage,
		FileTransferLogLevel:   j.FileTransferLogLevel,
		FileTransferWorkingDir: j.FileTransferWorkingDir,
		StatusSenderImage:      j.StatusSenderImage,
		ExternalID:             analysis.InvocationID,
		ImagePullSecretName:    j.ImagePullSecretName,
		BatchExitHandlerImage:  j.BatchExitHandlerImage,
	}

	maker := batch.NewWorkflowMaker(j.imageInfoGetter, analysis, j.clientset)
	workflow, err := maker.NewWorkflow(ctx, opts)

	if err != nil {
		return err
	}

	ctx, cl, err := batch.NewWorkflowServiceClient(ctx)
	if err != nil {
		return err
	}

	if _, err = batch.SubmitWorkflow(ctx, cl, workflow); err != nil {
		return err
	}

	log.Infof("launched with %f millicores reserved", millicoresReserved)

	return nil
}
