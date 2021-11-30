package adapter

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"

	"github.com/cyverse-de/jex-adapter/logging"
	"github.com/cyverse-de/jex-adapter/millicores"
	"github.com/cyverse-de/jex-adapter/types"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/streadway/amqp"
	"gopkg.in/cyverse-de/messaging.v6"
	"gopkg.in/cyverse-de/model.v4"
)

var log = logging.Log.WithFields(logrus.Fields{"package": "adapter"})

type Messenger interface {
	Launch(job *model.Job) error
	Stop(id string) error
}

func amqpError(err error) {
	amqpErr, ok := err.(*amqp.Error)
	if !ok || amqpErr.Code != 0 {
		log.Fatal(err)
	}
}

type AMQPMessenger struct {
	exchange string
	client   *messaging.Client
}

func NewAMQPMessenger(exchange string, client *messaging.Client) *AMQPMessenger {
	return &AMQPMessenger{
		exchange: exchange,
		client:   client,
	}
}

func (a *AMQPMessenger) Stop(id string) error {
	err := a.client.SendStopRequest(id, "root", "because I said to")
	if err != nil {
		amqpError(err)
	}
	return err
}

func (a *AMQPMessenger) Launch(job *model.Job) error {
	var (
		err        error
		launchJSON []byte
	)

	// This is included just for the side effect of creating the stop
	// queue before the job launches, otherwise some stop requests can be missed.
	s, err := a.client.CreateQueue(
		messaging.StopQueueName(job.InvocationID),
		a.exchange,
		messaging.StopRequestKey(job.InvocationID),
		false,
		true,
	)
	defer s.Close()

	if err != nil {
		amqpError(err)
		return err
	}

	if launchJSON, err = json.Marshal(messaging.NewLaunchRequest(job)); err != nil {
		return err
	}

	if err = a.client.Publish(messaging.LaunchesKey, launchJSON); err != nil {
		amqpError(err)
		return err
	}

	return nil
}

// JEXAdapter contains the application state for jex-adapter.
type JEXAdapter struct {
	cfg       *viper.Viper
	detector  *millicores.Detector
	messenger Messenger
}

// New returns a *JEXAdapter
func New(cfg *viper.Viper, detector *millicores.Detector, messenger Messenger) *JEXAdapter {
	return &JEXAdapter{
		cfg:       cfg,
		messenger: messenger,
		detector:  detector,
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

	log.Debug("starting sending stop message")
	err = j.messenger.Stop(invID)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("Done sending stop message")

	log.Info("sent stop message")

	return c.NoContent(http.StatusOK)
}

func (j *JEXAdapter) LaunchHandler(c echo.Context) error {
	request := c.Request()

	log := log.WithFields(logrus.Fields{"context": "app launch"})

	log.Debug("reading request body")
	bodyBytes, err := ioutil.ReadAll(request.Body)
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

	log.Debug("sending launch message")
	if err = j.messenger.Launch(job); err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done sending launch message")

	log.Debug("finding number of millicores reserved")
	millicoresReserved, err := j.detector.NumberReserved(job)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done finding number of millicores reserved")

	log.Debug("storing millicores reserved")
	if err = j.detector.StoreMillicoresReserved(c.Request().Context(), job, millicoresReserved); err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done storing millicores reserved")

	log.Infof("launched with %f millicores reserved", millicoresReserved)

	return c.NoContent(http.StatusOK)
}
