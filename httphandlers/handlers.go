package httphandlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/cyverse-de/app-exposer/adapter"
	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
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
//  3. Return (nil, nil) only when no operator has the analysis.
//
// A non-nil error indicates the lookup could not be completed (e.g. a
// database outage) and the caller should surface that to the client rather
// than treat the analysis as missing. Callers requiring a live client must
// treat nil-client-with-nil-error as "not found". Callers answering an
// "exists?" question may use nil-with-nil-error as "no".
func (h *HTTPHandlers) operatorClientForAnalysis(ctx context.Context, analysisID string) (*operatorclient.Client, error) {
	// Fast path: check the DB for a recorded operator name. GetOperatorName
	// normalizes sql.ErrNoRows to ("", nil); a non-nil error here signals a
	// real DB fault, which we must surface instead of silently falling through
	// to fan-out search. Under burst traffic (e.g. workshop launches), a brief
	// DB blip otherwise amplifies into an operator fan-out storm.
	operatorName, err := h.apps.GetOperatorName(ctx, analysisID)
	if err != nil {
		return nil, fmt.Errorf("looking up operator for analysis %s: %w", analysisID, err)
	}

	if operatorName != "" {
		client := h.scheduler.ClientByName(operatorName)
		if client != nil {
			log.Debugf("analysis %s routed to operator %q (fast path)", analysisID, operatorName)
			return client, nil
		}
		log.Warnf("operator %q not found in scheduler for analysis %s, searching all operators", operatorName, analysisID)
	} else {
		log.Debugf("no operator name recorded for analysis %s, searching all operators", analysisID)
	}

	// Search path: ask every operator in parallel whether it has this analysis.
	client, searchErr := h.searchOperatorsForAnalysis(ctx, analysisID)
	if client == nil {
		if searchErr != nil {
			// Some operators errored AND none reported found. We can't
			// tell whether the analysis is genuinely missing or hiding
			// on a degraded cluster, so surface the error rather than
			// letting the handler return a misleading 404.
			return nil, searchErr
		}
		log.Warnf("no operator has analysis %s", analysisID)
		return nil, nil
	}

	// Update the DB so future lookups use the fast path.
	if err := h.apps.SetOperatorName(ctx, analysisID, client.Name()); err != nil {
		log.Errorf("failed to record operator %q for analysis %s: %v", client.Name(), analysisID, err)
	}

	log.Infof("analysis %s found on operator %q (search path)", analysisID, client.Name())
	return client, nil
}

// searchOperatorsForAnalysis queries all configured operators in parallel
// to find which one is running the given analysis. Returns (client, nil)
// on success. Returns (nil, nil) when every operator responded and none
// has the analysis — truly not found. Returns (nil, err) only when one
// or more operators errored AND nothing reported found: in that case we
// cannot distinguish "not running anywhere" from "hiding on a degraded
// cluster", so the caller should surface the ambiguity (typically as 502)
// rather than return 404.
func (h *HTTPHandlers) searchOperatorsForAnalysis(ctx context.Context, analysisID string) (*operatorclient.Client, error) {
	type result struct {
		client *operatorclient.Client
		found  bool
		err    error
	}

	clients := h.scheduler.Clients()
	if len(clients) == 0 {
		return nil, nil
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
				ch <- result{client: c, err: err}
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

	var failed []string
	for r := range ch {
		if r.err != nil {
			failed = append(failed, r.client.Name())
			continue
		}
		if r.found {
			cancel() // Signal remaining goroutines to stop.
			return r.client, nil
		}
	}

	// Nothing found. If any operator failed during the search, report the
	// outage so the caller can return 502 rather than a misleading 404.
	if len(failed) > 0 {
		return nil, fmt.Errorf("analysis %s not found; %d operator(s) could not be checked: %v", analysisID, len(failed), failed)
	}
	return nil, nil
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

// aggregatedFailureResponse is the JSON body returned when every configured
// operator failed and no listing data is available.
type aggregatedFailureResponse struct {
	Message        string          `json:"error"`
	OperatorErrors []OperatorError `json:"operator_errors"`
}

// respondAggregated writes the response for an aggregateListing result. When
// no results are present and at least one operator errored, it returns 502
// Bad Gateway — the alternative (200 with an empty list) silently hides a
// cluster-wide outage. Callers pass the populated response body whose type
// is expected to carry a JSON-omitempty OperatorErrors field so partial-
// success can be surfaced without changing the 200 path for healthy clusters.
func respondAggregated(c echo.Context, body any, opErrs []OperatorError, hasResults bool) error {
	if !hasResults && len(opErrs) > 0 {
		return c.JSON(http.StatusBadGateway, aggregatedFailureResponse{
			Message:        "all operators unreachable or returned errors",
			OperatorErrors: opErrs,
		})
	}
	return c.JSON(http.StatusOK, body)
}

// ResourceInfoResponse wraps reporting.ResourceInfo with the list of
// per-operator errors encountered during aggregation. Consumers that care
// only about the resource data can keep ignoring the new field thanks to
// omitempty.
type ResourceInfoResponse struct {
	reporting.ResourceInfo
	OperatorErrors []OperatorError `json:"operator_errors,omitempty"`
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
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "analysis not found for external ID")
		}
		log.Errorf("error looking up analysis for external ID %s: %v", externalID, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to look up analysis")
	}

	client, err := h.operatorClientForAnalysis(ctx, analysisID)
	if err != nil {
		log.Errorf("operator routing unavailable for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "operator routing temporarily unavailable")
	}
	if client == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no operator found for analysis")
	}

	return fn(ctx, client, analysisID)
}

// routeAdminOperatorAction finds the operator running the analysis identified
// by the constants.AnalysisIDLabel path param and invokes fn. Intended for admin handlers
// that receive an analysis ID directly.
func (h *HTTPHandlers) routeAdminOperatorAction(c echo.Context, fn operatorAction) error {
	ctx := c.Request().Context()
	analysisID := c.Param(constants.AnalysisIDLabel)

	client, err := h.operatorClientForAnalysis(ctx, analysisID)
	if err != nil {
		log.Errorf("operator routing unavailable for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "operator routing temporarily unavailable")
	}
	if client == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no operator found for analysis")
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

	analysisID := c.Param(constants.AnalysisIDLabel)
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	externalID, err := h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, ExternalIDResp{ExternalID: externalID})
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
	externalID := c.QueryParam(constants.ExternalIDLabel)
	if externalID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "external-id not set")
	}

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		log.Error(err)
		return err
	}

	var userID string

	client, err := h.operatorClientForAnalysis(ctx, analysisID)
	if err != nil {
		log.Errorf("operator routing unavailable for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "operator routing temporarily unavailable")
	}
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
//	@Success		200	{array}		operatorclient.OperatorConfig
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

// CreateOperatorHandler adds a new operator to the database.
//
//	@ID				admin-create-operator
//	@Summary		Creates a new operator
//	@Description	Adds a new operator to the database.
//	@Accept			json
//	@Produce		json
//	@Param			body	body		operatorclient.OperatorConfig	true	"Operator to create"
//	@Success		201		{object}	operatorclient.OperatorConfig
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/admin/operators [post]
func (h *HTTPHandlers) CreateOperatorHandler(c echo.Context) error {
	ctx := c.Request().Context()

	// The request body has the same shape as the response — both are
	// operatorclient.OperatorConfig. A separate request type would only
	// invite the two to drift.
	var req operatorclient.OperatorConfig
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if strings.TrimSpace(req.Name) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name is required")
	}
	if strings.TrimSpace(req.URL) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url is required")
	}
	parsedURL, parseErr := url.Parse(req.URL)
	if parseErr != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url must be a valid HTTP(S) URL")
	}

	op := &db.Operator{
		Name:          req.Name,
		URL:           req.URL,
		TLSSkipVerify: req.TLSSkipVerify,
		Priority:      req.Priority,
	}

	created, err := h.db.InsertOperator(ctx, op)
	if err != nil {
		log.Errorf("failed to insert operator %q: %v", req.Name, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Return only the public fields — drop timestamps, IDs, and
	// reconciliation state. ToOperatorConfig performs that projection.
	return c.JSON(http.StatusCreated, created.ToOperatorConfig())
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
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete operator")
	}

	return c.NoContent(http.StatusOK)
}
