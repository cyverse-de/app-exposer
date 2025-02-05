package adapter

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/imageinfo"
	"github.com/cyverse-de/app-exposer/millicores"
	"github.com/cyverse-de/app-exposer/types"
	"github.com/cyverse-de/model/v7"
	"github.com/knadh/koanf"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
)

var log = common.Log

// JEXAdapter contains the application state for jex-adapter.
type JEXAdapter struct {
	cfg             *koanf.Koanf
	detector        *millicores.Detector
	imageInfoGetter imageinfo.InfoGetter
	filterFiles     []string
	logPath         string
	irodsBase       string
}

// New returns a *JEXAdapter
func New(cfg *koanf.Koanf, detector *millicores.Detector, imageInfoGetter imageinfo.InfoGetter) *JEXAdapter {
	return &JEXAdapter{
		cfg:             cfg,
		detector:        detector,
		imageInfoGetter: imageInfoGetter,
		logPath:         cfg.String("condor.log_path"),
		irodsBase:       cfg.String("irods.base"),
		filterFiles:     strings.Split(cfg.String("condor.filter_files"), ","),
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
	job, err := model.NewAnalysis(acfg, bodyBytes)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done parsing request body JSON")

	log = log.WithFields(logrus.Fields{"external_id": job.InvocationID})

	log.Debug("finding number of millicores reserved")
	millicoresReserved, err := j.detector.NumberReserved(job)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done finding number of millicores reserved")

	log.Infof("storing %s millicores reserved for %s", millicoresReserved.String(), job.InvocationID)
	if err = j.detector.StoreMillicoresReserved(ctx, job, millicoresReserved); err != nil {
		log.Error(err)
	}
	log.Infof("done storing %s millicores reserved for %s", millicoresReserved.String(), job.InvocationID)

	// TODO: Do launch logic

	log.Infof("launched with %f millicores reserved", millicoresReserved)

	return c.NoContent(http.StatusOK)
}
