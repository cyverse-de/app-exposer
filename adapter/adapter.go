package adapter

import (
	"errors"
	"io"
	"net/http"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/batch"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/imageinfo"
	"github.com/cyverse-de/app-exposer/millicores"
	"github.com/cyverse-de/app-exposer/quota"
	"github.com/cyverse-de/app-exposer/types"
	"github.com/cyverse-de/model/v7"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
)

var log = common.Log

// JEXAdapter contains the application state for jex-adapter.
type JEXAdapter struct {
	apps                   *apps.Apps
	detector               *millicores.Detector
	imageInfoGetter        imageinfo.InfoGetter
	filterFiles            []string
	logPath                string
	irodsBase              string
	fileTransferImage      string
	fileTransferWorkingDir string
	fileTransferLogLevel   string
	statusSenderImage      string
	namespace              string
	quotaEnforcer          *quota.Enforcer
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
}

// New returns a *JEXAdapter
func New(init *Init, apps *apps.Apps, detector *millicores.Detector, imageInfoGetter imageinfo.InfoGetter, enforcer *quota.Enforcer) *JEXAdapter {
	return &JEXAdapter{
		apps:                   apps,
		detector:               detector,
		imageInfoGetter:        imageInfoGetter,
		logPath:                init.LogPath,
		irodsBase:              init.IRODSBase,
		filterFiles:            init.FilterFiles,
		fileTransferImage:      init.FileTransferImage,
		fileTransferWorkingDir: init.FileTransferWorkingDir,
		fileTransferLogLevel:   init.FileTransferLogLevel,
		statusSenderImage:      init.StatusSenderImage,
		namespace:              init.Namespace,
		quotaEnforcer:          enforcer,
	}
}

func (j *JEXAdapter) Routes(router types.Router) types.Router {
	log := log.WithFields(logrus.Fields{"context": "adding routes"})

	router.GET("", j.HomeHandler)
	router.GET("/", j.HomeHandler)
	log.Info("added handler for GET /")

	router.POST("", j.LaunchHandler)
	router.POST("/", j.LaunchHandler)
	log.Info("added handler for POST /")

	router.DELETE("/stop/:invocation_id", j.StopHandler)
	log.Info("added handler for DELETE /stop/:invocation_id")

	return router
}

func (j *JEXAdapter) HomeHandler(c echo.Context) error {
	return c.String(http.StatusOK, "Welcome to the JEX.\n")
}

func (j *JEXAdapter) StopHandler(c echo.Context) error {
	var err error

	log := log.WithFields(logrus.Fields{"context": "stop app"})

	invID := c.Param("invocation_id")
	if invID == "" {
		err = errors.New("missing job id in URL")
		log.Error(err)
		return err
	}

	log = log.WithFields(logrus.Fields{"external_id": invID})

	// TODO: Add stop logic

	log.Info("sent stop message")

	return c.NoContent(http.StatusOK)
}

func (j *JEXAdapter) LaunchHandler(c echo.Context) error {
	request := c.Request()
	ctx := c.Request().Context()

	log := log.WithFields(logrus.Fields{"context": "app launch"})

	log.Debug("reading request body")
	bodyBytes, err := io.ReadAll(request.Body)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done reading request body")

	log.Debug("parsing request body JSON")
	acfg := &model.AnalysisConfig{
		LogPath:     j.logPath,
		FilterFiles: j.filterFiles,
		IRODSBase:   j.irodsBase,
	}
	analysis, err := model.NewAnalysis(acfg, bodyBytes)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done parsing request body JSON")

	log.Debug("validating analysis")
	if status, err := j.quotaEnforcer.ValidateJob(ctx, analysis, j.namespace); err != nil {
		if validationErr, ok := err.(common.ErrorResponse); ok {
			log.Error(validationErr)
			return validationErr
		}
		log.Error(err)
		return echo.NewHTTPError(status, err.Error())
	}
	log.Debug("done validating analysis")

	log = log.WithFields(logrus.Fields{"external_id": analysis.InvocationID})

	log.Debug("finding number of millicores reserved")
	millicoresReserved, err := j.detector.NumberReserved(analysis)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done finding number of millicores reserved")

	log.Infof("storing %s millicores reserved for %s", millicoresReserved.String(), analysis.InvocationID)
	if err = j.detector.StoreMillicoresReserved(ctx, analysis, millicoresReserved); err != nil {
		log.Error(err)
	}
	log.Infof("done storing %s millicores reserved for %s", millicoresReserved.String(), analysis.InvocationID)

	// Look up the analysis ID
	analysisID, err := j.apps.GetAnalysisIDByExternalID(ctx, analysis.InvocationID)
	if err != nil {
		log.Error(err)
		return err
	}

	// TODO: set the resource requests in the batch submissions

	opts := &batch.BatchSubmissionOpts{
		FileTransferImage:      j.fileTransferImage,
		FileTransferLogLevel:   j.fileTransferLogLevel,
		FileTransferWorkingDir: j.fileTransferWorkingDir,
		StatusSenderImage:      j.statusSenderImage,
		AnalysisID:             analysisID,
	}

	maker := batch.NewWorkflowMaker(j.imageInfoGetter, analysis)
	workflow := maker.NewWorkflow(opts)

	ctx, cl, err := batch.NewWorkflowServiceClient(ctx)
	if err != nil {
		log.Error(err)
		return err
	}

	if _, err = batch.SubmitWorkflow(ctx, cl, workflow); err != nil {
		log.Error(err)
		return err
	}

	log.Infof("launched with %f millicores reserved", millicoresReserved)

	return c.NoContent(http.StatusOK)
}
