package httpapi

import (
	"net/http"

	"github.com/agnos/agnoforge/internal/acquisition/app"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// listGaps answers GET /datasets/{p}/{s}/{tf}/gaps, optionally narrowed by
// ?status=. No status returns every Gap of the Dataset; a status outside the
// four canonical ones is a 400.
func (a *api) listGaps(w http.ResponseWriter, r *http.Request) {
	id, err := datasetFromPath(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	var filter app.GapFilter
	if raw := r.URL.Query().Get("status"); raw != "" {
		status, err := domain.ParseGapStatus(raw)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, err)
			return
		}
		filter.Status = &status
	}
	gaps, err := a.svc.Gaps(r.Context(), id, filter)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asGaps(gaps))
}

// patchGap answers PATCH /gaps/{id}: an operator's judgement on a Gap. Only
// open, ignored and unrecoverable are settable — repaired is the detector's
// word, so asking for it is a 400 — and the updated Gap comes back.
func (a *api) patchGap(w http.ResponseWriter, r *http.Request) {
	gapID, err := gapIDFromPath(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	var body gapPatchJSON
	if err := decodeJSON(r, &body); err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := domain.ParseGapStatus(body.Status)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.svc.SetGapStatus(r.Context(), gapID, status, body.Reason); err != nil {
		a.fail(w, err)
		return
	}
	gap, err := a.svc.Gap(r.Context(), gapID)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asGap(gap))
}

// repairGap answers POST /gaps/{id}/repair: a Backfill over exactly that
// Gap's range. It returns the Backfill's id to poll; 409 when that Dataset
// already has one running.
func (a *api) repairGap(w http.ResponseWriter, r *http.Request) {
	gapID, err := gapIDFromPath(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := a.svc.Repair(r.Context(), gapID)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusAccepted, repairJSON{BackfillID: string(status.ID)})
}
