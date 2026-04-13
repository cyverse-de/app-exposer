package httphandlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/cyverse-de/app-exposer/adapter"
	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/db"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/cyverse-de/model/v10"
	"github.com/labstack/echo/v4"
	"k8s.io/client-go/kubernetes"
)

var log = common.Log

// AnalysisLaunch is an alias for model.Analysis used as the HTTP request body
// for launching a VICE analysis.
type AnalysisLaunch model.Analysis

// HTTPHandlers holds the dependencies for all app-exposer HTTP handlers.
type HTTPHandlers struct {
	incluster    *incluster.Incluster
	apps         *apps.Apps
	clientset    kubernetes.Interface
	batchadapter *adapter.JEXAdapter
	scheduler    *operatorclient.Scheduler
	db           *db.Database
}

// New creates an HTTPHandlers with the provided dependencies injected.
func New(incluster *incluster.Incluster, apps *apps.Apps, clientset kubernetes.Interface, batchadapter *adapter.JEXAdapter, db *db.Database) *HTTPHandlers {
	return &HTTPHandlers{
		incluster:    incluster,
		apps:         apps,
		clientset:    clientset,
		batchadapter: batchadapter,
		db:           db,
	}
}

// SetScheduler configures the operator scheduler for multi-cluster routing.
// When set, launches and lifecycle operations are routed to remote operators.
func (h *HTTPHandlers) SetScheduler(s *operatorclient.Scheduler) {
	h.scheduler = s
	h.incluster.SetScheduler(s)
}

// GetScheduler returns the operator scheduler.
func (h *HTTPHandlers) GetScheduler() *operatorclient.Scheduler {
	return h.scheduler
}

// operatorClientForAnalysis looks up which operator is running an analysis
// and returns the corresponding client. Uses a three-step strategy:
//  1. Fast path: check the DB for a recorded operator name.
//  2. Search path: if no name is recorded or the named operator isn't found,
//     search all operators in parallel via HasAnalysis.
//  3. Return nil only if no operator has the analysis.
//
// Callers must treat a nil return as a fatal condition.
func (h *HTTPHandlers) operatorClientForAnalysis(ctx context.Context, analysisID string) *operatorclient.Client {
	// Fast path: check the DB for a recorded operator name.
	operatorName, err := h.apps.GetOperatorName(ctx, analysisID)
	if err != nil {
		log.Errorf("error looking up operator for analysis %s: %v", analysisID, err)
		// Fall through to search path.
	}

	if operatorName != "" {
		client := h.scheduler.ClientByName(operatorName)
		if client != nil {
			log.Debugf("analysis %s routed to operator %q (fast path)", analysisID, operatorName)
			return client
		}
		log.Warnf("operator %q not found in scheduler for analysis %s, searching all operators", operatorName, analysisID)
	} else {
		log.Debugf("no operator name recorded for analysis %s, searching all operators", analysisID)
	}

	// Search path: ask every operator in parallel whether it has this analysis.
	client := h.searchOperatorsForAnalysis(ctx, analysisID)
	if client == nil {
		log.Warnf("no operator has analysis %s", analysisID)
		return nil
	}

	// Update the DB so future lookups use the fast path.
	if err := h.apps.SetOperatorName(ctx, analysisID, client.Name()); err != nil {
		log.Errorf("failed to record operator %q for analysis %s: %v", client.Name(), analysisID, err)
	}

	log.Infof("analysis %s found on operator %q (search path)", analysisID, client.Name())
	return client
}

// searchOperatorsForAnalysis queries all configured operators in parallel to
// find which one is running the given analysis. Returns the first operator
// that reports having the analysis, or nil if none do.
func (h *HTTPHandlers) searchOperatorsForAnalysis(ctx context.Context, analysisID string) *operatorclient.Client {
	type result struct {
		client *operatorclient.Client
		found  bool
	}

	clients := h.scheduler.Clients()
	if len(clients) == 0 {
		return nil
	}

	// Use a cancellable child context so we can stop remaining searches
	// once we find the analysis.
	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan result, len(clients))

	// wg.Go (Go 1.25+) handles Add/Done internally. Range variables are
	// per-iteration in Go 1.22+ so no parameter capture is needed.
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Go(func() {
			found, err := c.HasAnalysis(searchCtx, analysisID)
			if err != nil {
				log.Warnf("search: operator %s error for analysis %s: %v", c.Name(), analysisID, err)
				ch <- result{client: c, found: false}
				return
			}
			ch <- result{client: c, found: found}
		})
	}

	// Close the channel once all goroutines finish to avoid leaks.
	go func() {
		wg.Wait()
		close(ch)
	}()

	for r := range ch {
		if r.found {
			cancel() // Signal remaining goroutines to stop.
			return r.client
		}
	}

	return nil
}

// aggregateListing queries all configured operators in parallel, applying the
// provided filters, and merges the results into a single ResourceInfo.
// Partial results are returned if some operators are unreachable.
func (h *HTTPHandlers) aggregateListing(ctx context.Context, params url.Values) (*reporting.ResourceInfo, []OperatorError, error) {
	merged := reporting.NewResourceInfo()
	clients := h.scheduler.Clients()
	if len(clients) == 0 {
		return merged, nil, nil
	}

	type result struct {
		info *reporting.ResourceInfo
		name string
		err  error
	}
	results := make([]result, len(clients))

	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Go(func() {
			info, err := client.Listing(ctx, params)
			results[i] = result{info: info, name: client.Name(), err: err}
		})
	}
	wg.Wait()

	var opErrs []OperatorError
	for _, r := range results {
		if r.err != nil {
			log.Errorf("error listing analyses from operator %s: %v", r.name, r.err)
			opErrs = append(opErrs, OperatorError{Operator: r.name, Error: r.err.Error()})
			continue
		}
		merged.Deployments = append(merged.Deployments, r.info.Deployments...)
		merged.Pods = append(merged.Pods, r.info.Pods...)
		merged.ConfigMaps = append(merged.ConfigMaps, r.info.ConfigMaps...)
		merged.Services = append(merged.Services, r.info.Services...)
		merged.Ingresses = append(merged.Ingresses, r.info.Ingresses...)
		merged.Routes = append(merged.Routes, r.info.Routes...)
	}

	reporting.SortByCreationTime(merged)
	return merged, opErrs, nil
}

// OperatorError represents an error returned by an operator during a listing
// request.
type OperatorError struct {
	Operator string `json:"operator"`
	Error    string `json:"error"`
}

// operatorAction is a function that performs an operation on an operator client
// for a given analysis. Used by routeOperatorAction and routeAdminOperatorAction
// to eliminate boilerplate in handlers that resolve an ID and forward to an operator.
type operatorAction func(ctx context.Context, client *operatorclient.Client, analysisID string) error

// routeOperatorAction resolves an external ID to an analysis ID, finds the
// operator running it, and invokes fn. Intended for user-facing handlers that
// receive an external ID via path param "id".
func (h *HTTPHandlers) routeOperatorAction(c echo.Context, fn operatorAction) error {
	ctx := c.Request().Context()
	externalID := c.Param("id")

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	return fn(ctx, client, analysisID)
}

// routeAdminOperatorAction finds the operator running the analysis identified
// by the "analysis-id" path param and invokes fn. Intended for admin handlers
// that receive an analysis ID directly.
func (h *HTTPHandlers) routeAdminOperatorAction(c echo.Context, fn operatorAction) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	return fn(ctx, client, analysisID)
}

// ExternalIDResp is the response body for the AdminGetExternalIDHandler endpoint.
type ExternalIDResp struct {
	ExternalID string `json:"external_id" example:"bb52aefb-e021-4ece-89e5-fd73ce30643c"`
}

// AdminGetExternalIDHandler returns the external ID associated with the analysis ID.
// There is only one external ID for each VICE analysis, unlike non-VICE analyses.
//
//	@ID				admin-get-external-id
//	@Summary		Returns external ID
//	@Description	Returns the external ID associated with the provided analysis ID.
//	@Description	Only returns the first external ID in multi-step analyses.
//	@Produces		json
//	@Param			analysis-id	path		string	true	"analysis UUID"	minLength(36)	maxLength(36)
//	@Success		200			{object}	ExternalIDResp
//	@Failure		500			{object}	common.ErrorResponse
//	@Failure		400			{object}	common.ErrorResponse	"id parameter is empty"
//	@Router			/vice/admin/analyses/{analysis-id}/external-id [get]
func (h *HTTPHandlers) AdminGetExternalIDHandler(c echo.Context) error {
	ctx := c.Request().Context()

	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	externalID, err := h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, ExternalIDResp{ExternalID: externalID})
}

// ApplyAsyncLabelsHandler is the http handler for triggering the application
// of labels on running VICE analyses.
//
//	@ID				apply-async-labels
//	@Summary		Applies labels to running VICE analyses.
//	@Description	Asynchronously applies labels to all running VICE analyses.
//	@Description	The application of the labels may not be complete by the time the response is returned.
//	@Success		200
//	@Failure		500	{object}	common.ErrorResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Router			/vice/apply-labels [post]
func (h *HTTPHandlers) ApplyAsyncLabelsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	errs := h.incluster.ApplyAsyncLabels(ctx)

	if len(errs) > 0 {
		var errMsg strings.Builder
		for _, err := range errs {
			log.Error(err)
			_, _ = fmt.Fprintf(&errMsg, "%s\n", err.Error())
		}

		return c.String(http.StatusInternalServerError, errMsg.String())
	}
	return c.NoContent(http.StatusOK)
}

// AsyncData contains metadata that is computed asynchronously after job launch:
// the analysis ID, the routing subdomain, and the user's login IP.
type AsyncData struct {
	AnalysisID string `json:"analysisID"`
	Subdomain  string `json:"subdomain"`
	IPAddr     string `json:"ipAddr"`
}

// AsyncDataHandler returns data that is generated asynchronously from the job launch.
//
//	@ID				async-data
//	@Summary		Returns data that is generated asynchronously from the job launch.
//	@Description	Returns data that is applied to analyses outside of an API call.
//	@Description	The returned data is not returned asynchronously, despite the name of the call.
//	@Param			external-id	query		string	true	"External ID"
//	@Success		200			{object}	AsyncData
//	@Failure		500			{object}	common.ErrorResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Router			/vice/async-data [get]
func (h *HTTPHandlers) AsyncDataHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.QueryParam("external-id")
	if externalID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "external-id not set")
	}

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		log.Error(err)
		return err
	}

	var userID string

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusNotFound, "analysis not found on any operator")
	}

	// The operator status includes the deployment names. We need the labels.
	// Since we can't easily get labels from the operator Status endpoint,
	// we'll use the Listing endpoint which is more verbose but has everything.
	info, err := client.Listing(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	for _, d := range info.Deployments {
		if d.ExternalID == externalID {
			userID = d.UserID
			break
		}
	}

	if userID == "" {
		return echo.NewHTTPError(http.StatusNotFound, "user-id not found for analysis")
	}

	subdomain := common.Subdomain(userID, externalID)
	ipAddr, err := h.apps.GetUserIP(ctx, userID)
	if err != nil {
		log.Error(err)
		return err
	}

	return c.JSON(http.StatusOK, AsyncData{
		AnalysisID: analysisID,
		Subdomain:  subdomain,
		IPAddr:     ipAddr,
	})
}

// AdminOperatorsHandler lists all operators registered in the database,
// returning only their name, URL, and TLS skip-verify flag.
//
//	@ID				admin-list-operators
//	@Summary		Lists registered operators
//	@Description	Returns the name, URL, and tls_skip_verify flag for all operators in the database.
//	@Produce		json
//	@Success		200	{array}		db.OperatorSummary
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/operators [get]
func (h *HTTPHandlers) AdminOperatorsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	ops, err := h.db.ListOperatorSummaries(ctx)
	if err != nil {
		log.Errorf("failed to list operators: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, ops)
}

// OperatorCapacity contains the capacity status of an operator or an error
// if it could not be reached.
type OperatorCapacity struct {
	Operator string                           `json:"operator"`
	Capacity *operatorclient.CapacityResponse `json:"capacity,omitempty"`
	Error    string                           `json:"error,omitempty"`
}

// AdminOperatorCapacitiesHandler returns the live capacity status of all
// configured operators by querying each one in parallel.
//
//	@ID				admin-operator-capacities
//	@Summary		Returns operator capacities
//	@Description	Queries each configured operator's capacity endpoint in parallel and returns the results.
//	@Produce		json
//	@Success		200	{array}		OperatorCapacity
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/operators/capacities [get]
func (h *HTTPHandlers) AdminOperatorCapacitiesHandler(c echo.Context) error {
	if h.scheduler == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "operator scheduler not configured")
	}

	ctx := c.Request().Context()
	clients := h.scheduler.Clients()

	results := make([]OperatorCapacity, len(clients))
	var wg sync.WaitGroup

	for i, client := range clients {
		wg.Add(1)
		go func(idx int, cl *operatorclient.Client) {
			defer wg.Done()
			capResp, err := cl.Capacity(ctx)
			status := OperatorCapacity{Operator: cl.Name()}
			if err != nil {
				status.Error = err.Error()
			} else {
				status.Capacity = capResp
			}
			results[idx] = status
		}(i, client)
	}
	wg.Wait()

	return c.JSON(http.StatusOK, results)
}

// createOperatorRequest is the JSON request body for creating a new operator.
type createOperatorRequest struct {
	Name                  string `json:"name"`
	URL                   string `json:"url"`
	TLSSkipVerify         bool   `json:"tls_skip_verify"`
	AuthUser              string `json:"auth_user"`
	AuthPasswordEncrypted string `json:"auth_password_encrypted"`
	Priority              int    `json:"priority"`
}

// CreateOperatorHandler adds a new operator to the database.
// The auth_password_encrypted field is assumed to be pre-encrypted by the caller.
//
//	@ID				admin-create-operator
//	@Summary		Creates a new operator
//	@Description	Adds a new operator to the database. The password must be pre-encrypted.
//	@Accept			json
//	@Produce		json
//	@Param			body	body		createOperatorRequest	true	"Operator to create"
//	@Success		201		{object}	db.OperatorSummary
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/admin/operators [post]
func (h *HTTPHandlers) CreateOperatorHandler(c echo.Context) error {
	ctx := c.Request().Context()

	var req createOperatorRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if strings.TrimSpace(req.Name) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name is required")
	}
	if strings.TrimSpace(req.URL) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url is required")
	}
	if strings.TrimSpace(req.AuthUser) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "auth_user is required")
	}
	if strings.TrimSpace(req.AuthPasswordEncrypted) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "auth_password_encrypted is required")
	}

	op := &db.Operator{
		Name:                  req.Name,
		URL:                   req.URL,
		TLSSkipVerify:         req.TLSSkipVerify,
		AuthUser:              req.AuthUser,
		AuthPasswordEncrypted: req.AuthPasswordEncrypted,
		Priority:              req.Priority,
	}

	created, err := h.db.InsertOperator(ctx, op)
	if err != nil {
		log.Errorf("failed to insert operator %q: %v", req.Name, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Return only non-sensitive fields.
	return c.JSON(http.StatusCreated, db.OperatorSummary{
		Name:          created.Name,
		URL:           created.URL,
		TLSSkipVerify: created.TLSSkipVerify,
		Priority:      created.Priority,
	})
}

// DeleteOperatorHandler deletes an operator by name. The operation is
// idempotent for operators with no associated jobs: deleting a non-existent
// operator returns 200. Deleting an operator that still has jobs referencing
// it will fail due to a foreign key constraint.
//
//	@ID				admin-delete-operator
//	@Summary		Deletes an operator by name
//	@Description	Removes the named operator from the database. Succeeds silently if the operator does not exist. Fails if jobs still reference the operator.
//	@Param			name	path	string	true	"Operator name"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/operators/name/{name} [delete]
func (h *HTTPHandlers) DeleteOperatorHandler(c echo.Context) error {
	ctx := c.Request().Context()

	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name is required")
	}

	if err := h.db.DeleteOperatorByName(ctx, name); err != nil {
		log.Errorf("failed to delete operator %q: %v", name, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.NoContent(http.StatusOK)
}
