package httphandlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/cyverse-de/model/v7"
	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func (h *HTTPHandlers) BatchHomeHandler(c echo.Context) error {
	return c.String(http.StatusOK, "Welcome to the JEX.\n")
}

type uuidBody struct {
	UUID string
}

func (h *HTTPHandlers) BatchStopByUUID(c echo.Context) error {
	var (
		err error
		b   uuidBody
	)

	ctx := c.Request().Context()

	if err = json.NewDecoder(c.Request().Body).Decode(&b); err != nil {
		return err
	}

	if err = h.batchadapter.StopWorkflow(ctx, b.UUID); err != nil {
		log.Error(err)
		return err
	}

	return c.NoContent(http.StatusOK)
}

func (h *HTTPHandlers) BatchStopHandler(c echo.Context) error {
	var err error

	log := log.WithFields(logrus.Fields{"context": "stop app"})

	externalID := c.Param("id")
	if externalID == "" {
		err = errors.New("missing external id in URL")
		log.Error(err)
		return err
	}

	log = log.WithFields(logrus.Fields{"external_id": externalID})

	ctx := c.Request().Context()

	if err = h.batchadapter.StopWorkflow(ctx, externalID); err != nil {
		log.Error(err)
		return err
	}

	log.Info("sent stop message")

	return c.NoContent(http.StatusOK)
}

func (h *HTTPHandlers) BatchLaunchHandler(c echo.Context) error {
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
		LogPath:     h.batchadapter.LogPath,
		FilterFiles: h.batchadapter.FilterFiles,
		IRODSBase:   h.batchadapter.IRODSBase,
	}
	analysis, err := model.NewAnalysis(acfg, bodyBytes)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Debug("done parsing request body JSON")

	if err = h.batchadapter.LaunchWorkflow(ctx, analysis); err != nil {
		log.Error(err)
		return err
	}

	return c.NoContent(http.StatusOK)
}
