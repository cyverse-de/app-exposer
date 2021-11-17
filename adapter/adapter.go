package adapter

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/spf13/viper"
	"github.com/streadway/amqp"
)

// JEXAdapter contains the application state for jex-adapter.
type JEXAdapter struct {
	cfg    *viper.Viper
	client *messaging.Client
}

// New returns a *JEXAdapter
func New(cfg *viper.Viper) *JEXAdapter {
	return &JEXAdapter{
		cfg: cfg,
	}
}

func amqpError(err error) {
	amqpErr, ok := err.(*amqp.Error)
	if !ok || amqpErr.Code != 0 {
		logcabin.Error.Fatal(err)
	}
}

func (j *JEXAdapter) home(writer http.ResponseWriter, request *http.Request) {
	fmt.Fprintf(writer, "Welcome to the JEX.\n")
}

func (j *JEXAdapter) stop(writer http.ResponseWriter, request *http.Request) {
	var (
		invID string
		ok    bool
		err   error
		v     = mux.Vars(request)
	)

	logcabin.Info.Printf("Request received:\n%#v\n", request)

	logcabin.Info.Println("Getting invocation ID out of the Vars")
	if invID, ok = v["invocation_id"]; !ok {
		http.Error(writer, "Missing job id in URL", http.StatusBadRequest)
		logcabin.Error.Print("Missing job id in URL")
		return
	}
	logcabin.Info.Printf("Invocation ID is %s\n", invID)

	logcabin.Info.Println("Sending stop request")
	err = j.client.SendStopRequest(invID, "root", "because I said to")
	if err != nil {
		http.Error(
			writer,
			fmt.Sprintf("Error sending stop request %s", err.Error()),
			http.StatusInternalServerError,
		)
		amqpError(err)
		return
	}
	logcabin.Info.Println("Done sending stop request")
}

func (j *JEXAdapter) launch(writer http.ResponseWriter, request *http.Request) {
	bodyBytes, err := ioutil.ReadAll(request.Body)
	if err != nil {
		logcabin.Error.Print(err)
		http.Error(writer, "Request had no body", http.StatusBadRequest)
		return
	}

	job, err := model.NewFromData(j.cfg, bodyBytes)
	if err != nil {
		logcabin.Error.Print(err)
		http.Error(
			writer,
			fmt.Sprintf("Failed to create job from json: %s", err.Error()),
			http.StatusBadRequest,
		)
		return
	}

	// Create the stop request channel
	stopRequestChannel, err := j.client.CreateQueue(
		messaging.StopQueueName(job.InvocationID),
		exchangeName,
		messaging.StopRequestKey(job.InvocationID),
		false,
		true,
	)
	if err != nil {
		logcabin.Error.Print(err)
		http.Error(
			writer,
			fmt.Sprintf("Error creating stop request queue: %s", err.Error()),
			http.StatusInternalServerError,
		)
		amqpError(err)
	}
	defer stopRequestChannel.Close()

	launchRequest := messaging.NewLaunchRequest(job)
	if err != nil {
		logcabin.Error.Print(err)
		http.Error(
			writer,
			fmt.Sprintf("Error creating launch request: %s", err.Error()),
			http.StatusInternalServerError,
		)
		amqpError(err)
		return
	}

	launchJSON, err := json.Marshal(launchRequest)
	if err != nil {
		logcabin.Error.Print(err)
		http.Error(
			writer,
			fmt.Sprintf("Error creating launch request JSON: %s", err.Error()),
			http.StatusInternalServerError,
		)
		return
	}

	err = j.client.Publish(messaging.LaunchesKey, launchJSON)
	if err != nil {
		logcabin.Error.Print(err)
		http.Error(
			writer,
			fmt.Sprintf("Error publishing launch request: %s", err.Error()),
			http.StatusInternalServerError,
		)
		amqpError(err)
		return
	}
}
