package httphandlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/labstack/echo/v4"
)

// CanonicalURLResponse is the success body for AdminCanonicalURLHandler.
type CanonicalURLResponse struct {
	URL string `json:"url"`
}

// AdminCanonicalURLHandler returns the user-facing URL that should serve the
// VICE analysis whose subdomain matches the `:host` path parameter. The URL
// is constructed from the matching operator's user-facing base URL (the
// `operators.base_url` column), so a request that landed on the wrong
// cluster's wildcard ingress can be redirected to the cluster that actually
// runs the analysis.
//
// The default backend on each cluster catches the `*.<vice-domain>` wildcard
// and serves a waiting page when no analysis-specific HTTPRoute exists. With
// multi-cluster routing, that wildcard now traps requests for analyses that
// live on a different cluster; this endpoint is the lookup that lets the
// default backend recover by 302-ing the browser to the right base URL.
//
//	@ID				admin-canonical-url
//	@Summary		Resolve a VICE subdomain to its canonical cross-cluster URL
//	@Description	Searches every configured operator for the analysis with the
//	@Description	given subdomain. On match, returns the URL built from that
//	@Description	operator's base_url. Returns 404 when no operator owns the
//	@Description	subdomain or the owning operator has no base_url. Returns
//	@Description	502 when every operator was unreachable.
//	@Produce		json
//	@Param			host	path		string	true	"The analysis subdomain"
//	@Success		200		{object}	CanonicalURLResponse
//	@Failure		404		{object}	common.ErrorResponse
//	@Failure		502		{object}	aggregatedFailureResponse
//	@Router			/vice/admin/{host}/canonical-url [get]
func (h *HTTPHandlers) AdminCanonicalURLHandler(c echo.Context) error {
	ctx := c.Request().Context()
	subdomain := c.Param("host")
	if subdomain == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "host path parameter is required")
	}

	client, opErrs, err := h.findOperatorForSubdomain(ctx, subdomain)
	if err != nil {
		return err
	}
	if client == nil {
		if len(opErrs) > 0 {
			log.Errorf("canonical-url lookup degraded for subdomain %s: %+v", subdomain, opErrs)
			return c.JSON(http.StatusBadGateway, aggregatedFailureResponse{
				Message:        "all operators unreachable or returned errors",
				OperatorErrors: opErrs,
			})
		}
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("no operator owns subdomain %s", subdomain))
	}

	op, err := h.db.GetOperatorByID(ctx, client.ID())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The scheduler knows about this operator but the row was deleted
			// out from under it (unlikely under ON DELETE RESTRICT, but
			// possible if the scheduler is mid-resync). Treat as not-found
			// from the caller's perspective so the default backend falls
			// through to its waiting page instead of returning a 500.
			log.Warnf("canonical-url: operator %s (id=%s) owns subdomain %s but no DB row found", client.Name(), client.ID(), subdomain)
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("no operator owns subdomain %s", subdomain))
		}
		log.Errorf("canonical-url: looking up operator %s: %v", client.ID(), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "operator lookup failed")
	}
	if op.BaseURL == nil {
		log.Warnf("canonical-url: operator %s (id=%s) has no base_url; cannot build canonical URL for subdomain %s", op.Name, op.ID, subdomain)
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("operator %s has no base_url configured", op.Name))
	}

	canonical, err := buildCanonicalURL(*op.BaseURL, subdomain)
	if err != nil {
		log.Errorf("canonical-url: building URL from base_url %q for operator %s: %v", *op.BaseURL, op.Name, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not build canonical URL")
	}

	return c.JSON(http.StatusOK, CanonicalURLResponse{URL: canonical})
}

// findOperatorForSubdomain queries every configured operator in parallel for
// the given subdomain and returns the first client whose listing contained
// any deployments. Returns nil client with a (possibly empty) opErrs slice
// when no operator claims the subdomain.
func (h *HTTPHandlers) findOperatorForSubdomain(ctx context.Context, subdomain string) (*operatorclient.Client, []OperatorError, error) {
	clients := h.scheduler.Clients()
	if len(clients) == 0 {
		return nil, nil, nil
	}

	params := url.Values{}
	params.Set(constants.SubdomainLabel, subdomain)

	type result struct {
		info *reporting.ResourceInfo
		err  error
	}
	results := make([]result, len(clients))

	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Go(func() {
			info, err := client.Listing(ctx, params)
			results[i] = result{info: info, err: err}
		})
	}
	wg.Wait()

	var opErrs []OperatorError
	var match *operatorclient.Client
	for i, r := range results {
		if r.err != nil {
			opErrs = append(opErrs, OperatorError{Operator: clients[i].Name(), Error: r.err.Error()})
			continue
		}
		if match == nil && r.info != nil && len(r.info.Deployments) > 0 {
			match = clients[i]
		}
	}
	return match, opErrs, nil
}

// buildCanonicalURL takes an operator base URL (e.g. "https://sandbox.cyverse.rocks"
// or "https://cyverse.run:4343") and prepends the analysis subdomain as a new
// leftmost label of the host, preserving scheme, port, and trailing slash.
// Mirrors the algorithm in apps/src/apps/clients/notifications.clj:interapps-url.
func buildCanonicalURL(baseURL, subdomain string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL: %w", err)
	}
	hostname := u.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("base URL %q has no host", baseURL)
	}
	newHost := subdomain + "." + hostname
	if port := u.Port(); port != "" {
		newHost = newHost + ":" + port
	}
	u.Host = newHost
	return u.String(), nil
}
