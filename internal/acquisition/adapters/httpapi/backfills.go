package httpapi

import (
	"fmt"
	"net/http"

	"github.com/agnos/agnoforge/internal/acquisition/app"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// listProviders answers GET /providers with every Provider the service was
// built with and the Timeframes it offers.
func (a *api) listProviders(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, asProviders(a.svc.Providers()))
}

// startBackfill answers POST /backfills. It returns as soon as the Backfill
// is registered, with the effective range — the requested one clipped up to
// the Provider's earliest available Bar — so the caller knows what will
// actually be acquired.
//
// An unknown Provider, Symbol or Timeframe and a malformed range are 400; a
// Dataset that already has a Backfill running is 409.
func (a *api) startBackfill(w http.ResponseWriter, r *http.Request) {
	var body backfillRequestJSON
	if err := decodeJSON(r, &body); err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	tf, err := domain.ParseTimeframe(body.Timeframe)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	requested, err := parseRange(body.Start, body.End)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}

	status, err := a.svc.StartBackfill(r.Context(), app.BackfillRequest{
		Provider:  body.Provider,
		Symbol:    domain.Symbol(body.Symbol),
		Timeframe: tf,
		Range:     requested,
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusAccepted, backfillStartedJSON{
		ID:             string(status.ID),
		EffectiveRange: asRange(status.Range),
	})
}

// showBackfill answers GET /backfills/{id} with the Backfill's progress.
func (a *api) showBackfill(w http.ResponseWriter, r *http.Request) {
	status, ok := a.svc.Backfill(app.BackfillID(r.PathValue("id")))
	if !ok {
		a.fail(w, fmt.Errorf("%w: backfill %q", domain.ErrNotFound, r.PathValue("id")))
		return
	}
	a.writeJSON(w, http.StatusOK, asBackfill(status))
}

// cancelBackfill answers DELETE /backfills/{id}. Cancellation is a request,
// not an event: the run stops at its next page, so the 202 carries the status
// as it stands and the caller polls for the terminal state.
func (a *api) cancelBackfill(w http.ResponseWriter, r *http.Request) {
	id := app.BackfillID(r.PathValue("id"))
	if err := a.svc.CancelBackfill(id); err != nil {
		a.fail(w, err)
		return
	}
	status, ok := a.svc.Backfill(id)
	if !ok {
		a.fail(w, fmt.Errorf("%w: backfill %q", domain.ErrNotFound, id))
		return
	}
	a.writeJSON(w, http.StatusAccepted, asBackfill(status))
}
