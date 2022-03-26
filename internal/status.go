package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path"

	"github.com/cyverse-de/messaging/v9"
	"github.com/pkg/errors"
)

// AnalysisStatusPublisher is the interface for types that need to publish a job
// update.
type AnalysisStatusPublisher interface {
	Fail(jobID, msg string) error
	Success(jobID, msg string) error
	Running(jobID, msg string) error
}

// JSLPublisher is a concrete implementation of AnalysisStatusPublisher that
// posts status updates to the job-status-listener service.
type JSLPublisher struct {
	statusURL string
}

// AnalysisStatus contains the data needed to post a status update to the
// notification-agent service.
type AnalysisStatus struct {
	Host    string
	State   messaging.JobState
	Message string
}

func (j *JSLPublisher) postStatus(jobID, msg string, jobState messaging.JobState) error {
	status := &AnalysisStatus{
		Host:    hostname(),
		State:   jobState,
		Message: msg,
	}

	u, err := url.Parse(j.statusURL)
	if err != nil {
		return errors.Wrapf(
			err,
			"error parsing URL %s for job %s before posting %s status",
			j,
			jobID,
			jobState,
		)
	}
	u.Path = path.Join(jobID, "status")

	js, err := json.Marshal(status)
	if err != nil {
		return errors.Wrapf(
			err,
			"error marshalling JSON for analysis %s before posting %s status",
			jobID,
			jobState,
		)

	}
	response, err := http.Post(u.String(), "application/json", bytes.NewReader(js))
	if err != nil {
		return errors.Wrapf(
			err,
			"error returned posting %s status for job %s to %s",
			jobState,
			jobID,
			u.String(),
		)
	}
	if response.StatusCode < 200 || response.StatusCode > 399 {
		return errors.Wrapf(
			err,
			"error status code %d returned after posting %s status for job %s to %s: %s",
			response.StatusCode,
			jobState,
			jobID,
			u.String(),
			response.Body,
		)
	}
	return nil
}

// Fail sends an analysis failure update with the provided message via the AMQP
// broker. Should be sent once.
func (j *JSLPublisher) Fail(jobID, msg string) error {
	log.Warnf("Sending failure job status update for external-id %s", jobID)

	return j.postStatus(jobID, msg, messaging.FailedState)
}

// Success sends a success update via the AMQP broker. Should be sent once.
func (j *JSLPublisher) Success(jobID, msg string) error {
	log.Warnf("Sending success job status update for external-id %s", jobID)

	return j.postStatus(jobID, msg, messaging.SucceededState)
}

// Running sends an analysis running status update with the provided message via the
// AMQP broker. May be sent multiple times, preferably with different messages.
func (j *JSLPublisher) Running(jobID, msg string) error {
	log.Warnf("Sending running job status update for external-id %s", jobID)
	return j.postStatus(jobID, msg, messaging.RunningState)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		log.Errorf("Couldn't get the hostname: %s", err.Error())
		return ""
	}
	return h
}
