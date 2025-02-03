package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/cockroachdb/apd"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/millicores"
	"github.com/cyverse-de/app-exposer/types"
	"github.com/cyverse-de/model/v6"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
)

var log = common.Log

const otelName = "github.com/cyverse-de/app-exposer/adapter"

type millicoresJob struct {
	ID                 uuid.UUID
	Job                model.Job
	MillicoresReserved *apd.Decimal
}

// JEXAdapter contains the application state for jex-adapter.
type JEXAdapter struct {
	cfg      *viper.Viper
	detector *millicores.Detector
	jobs     map[string]bool
	addJob   chan millicoresJob
	jobDone  chan uuid.UUID
	exit     chan bool
}

// New returns a *JEXAdapter
func New(cfg *viper.Viper, detector *millicores.Detector) *JEXAdapter {
	return &JEXAdapter{
		cfg:      cfg,
		detector: detector,
		addJob:   make(chan millicoresJob),
		jobDone:  make(chan uuid.UUID),
		exit:     make(chan bool),
		jobs:     map[string]bool{},
	}
}

func (j *JEXAdapter) Run() {
	for {
		select {
		case mj := <-j.addJob:
			j.jobs[mj.ID.String()] = true
			go func(mj millicoresJob) {
				ctx, span := otel.Tracer(otelName).Start(context.Background(), "millicores iteration")
				defer span.End()

				var err error

				log.Infof("storing %s millicores reserved for %s", mj.MillicoresReserved.String(), mj.Job.InvocationID)
				if err = j.detector.StoreMillicoresReserved(ctx, &mj.Job, mj.MillicoresReserved); err != nil {
					log.Error(err)
				}
				log.Infof("done storing %s millicores reserved for %s", mj.MillicoresReserved.String(), mj.Job.InvocationID)

				j.jobDone <- mj.ID
			}(mj)

		case doneJobID := <-j.jobDone:
			delete(j.jobs, doneJobID.String())

		case <-j.exit:
			break
		}
	}
}

func (j *JEXAdapter) StoreMillicoresReserved(job model.Job, millicoresReserved *apd.Decimal) error {
	newjob := millicoresJob{
		ID:                 uuid.New(),
		Job:                job,
		MillicoresReserved: millicoresReserved,
	}

	j.addJob <- newjob

	return nil
}

func (j *JEXAdapter) Finish() {
	j.exit <- true
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

	log := log.WithFields(logrus.Fields{"context": "app launch"})

	log.Debug("reading request body")
	bodyBytes, err := io.ReadAll(request.Body)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done reading request body")

	log.Debug("parsing request body JSON")
	job, err := model.NewFromData(j.cfg, bodyBytes)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done parsing request body JSON")

	log = log.WithFields(logrus.Fields{"external_id": job.InvocationID})

	// TODO: Do launch logic

	log.Debug("finding number of millicores reserved")
	millicoresReserved, err := j.detector.NumberReserved(job)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done finding number of millicores reserved")

	log.Debug("before asynchronous StoreMillicoresReserved call")
	if err = j.StoreMillicoresReserved(*job, millicoresReserved); err != nil {
		log.Error(err)
		return err
	}
	log.Debug("after asynchronous StoreMillicoresReserved call")

	log.Infof("launched with %f millicores reserved", millicoresReserved)

	return c.NoContent(http.StatusOK)
}
