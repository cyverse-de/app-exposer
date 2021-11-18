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

// JEXAdapter contains the application state for jex-adapter.
type JEXAdapter struct {
	cfg          *viper.Viper
	client       *messaging.Client
	detector     *millicores.Detector
	amqpURI      string
	exchangeName string
}

// New returns a *JEXAdapter
func New(cfg *viper.Viper, detector *millicores.Detector, amqpURI, exchangeName string) *JEXAdapter {

	return &JEXAdapter{
		cfg:          cfg,
		amqpURI:      amqpURI,
		exchangeName: exchangeName,
		detector:     detector,
	}
}

func amqpError(err error) {
	amqpErr, ok := err.(*amqp.Error)
	if !ok || amqpErr.Code != 0 {
		log.Fatal(err)
	}
}

func (j *JEXAdapter) Routes(router types.Router) {
	router.GET("", j.HomeHandler)
	router.GET("/", j.HomeHandler)
	router.POST("", j.LaunchHandler)
	router.POST("/", j.LaunchHandler)
	router.DELETE("/stop/:invocation_id", j.StopHandler)
}

func (j *JEXAdapter) HomeHandler(c echo.Context) error {
	return c.String(http.StatusOK, "Welcome to the JEX.\n")
}

func (j *JEXAdapter) StopHandler(c echo.Context) error {
	var err error

	log.Infof("Request received:\n%#v\n", c.Request())
	log.Info("Getting invocation ID out of the Vars")

	invID := c.Param("invocation_id")
	if invID == "" {
		err = errors.New("missing job id in URL")
		log.Error(err)
		return err
	}

	log.Infof("Invocation ID is %s\n", invID)

	log.Info("Sending stop request")
	err = j.client.SendStopRequest(invID, "root", "because I said to")
	if err != nil {
		log.Error(err)
		amqpError(err)
		return err
	}
	log.Info("Done sending stop request")

	return c.NoContent(http.StatusOK)
}

func (j *JEXAdapter) LaunchHandler(c echo.Context) error {
	request := c.Request()

	bodyBytes, err := ioutil.ReadAll(request.Body)
	if err != nil {
		log.Error(err)
		return err
	}

	job, err := model.NewFromData(j.cfg, bodyBytes)
	if err != nil {
		log.Error(err)
		return err
	}

	// Create the stop request channel
	stopRequestChannel, err := j.client.CreateQueue(
		messaging.StopQueueName(job.InvocationID),
		j.exchangeName,
		messaging.StopRequestKey(job.InvocationID),
		false,
		true,
	)

	defer stopRequestChannel.Close()

	if err != nil {
		log.Error(err)
		amqpError(err)
		return err
	}

	launchRequest := messaging.NewLaunchRequest(job)
	if err != nil {
		log.Error(err)
		amqpError(err)
		return err
	}

	launchJSON, err := json.Marshal(launchRequest)
	if err != nil {
		log.Error(err)
		return err
	}

	err = j.client.Publish(messaging.LaunchesKey, launchJSON)
	if err != nil {
		log.Error(err)
		amqpError(err)
		return err
	}

	millicoresReserved, err := j.detector.NumberReserved(job)
	if err != nil {
		log.Error(err)
		return err
	}

	if err = j.detector.StoreMillicoresReserved(c.Request().Context(), job, millicoresReserved); err != nil {
		log.Error(err)
		return err
	}

	return c.NoContent(http.StatusOK)
}
