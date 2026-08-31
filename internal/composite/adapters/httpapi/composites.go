package httpapi

import (
	"net/http"

	"github.com/agnos/agnoforge/internal/composite/domain"
)

// createComposite answers POST /composites: declare a Composite Dataset under
// a name. Nothing is built — the dataset is a draft until a Build runs.
//
// A malformed declaration is a 400; a name that is already taken is a 409,
// because the name is the identity and there can only be one.
func (a *api) createComposite(w http.ResponseWriter, r *http.Request) {
	var body createRequestJSON
	if err := decodeJSON(r, &body); err != nil {
		a.fail(w, err)
		return
	}
	name, err := domain.ParseName(body.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	cfg, err := body.configJSON.toConfig()
	if err != nil {
		a.fail(w, err)
		return
	}
	created, err := a.svc.Create(r.Context(), name, cfg)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, asComposite(created))
}

// listComposites answers GET /composites with every declared Composite
// Dataset, ordered by name. No datasets is [], not an error.
func (a *api) listComposites(w http.ResponseWriter, r *http.Request) {
	datasets, err := a.svc.Datasets(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asComposites(datasets))
}

// showComposite answers GET /composites/{name} with the full configuration,
// the lifecycle state, the ordered Segments the last Build assembled and the
// Quality it computed.
//
// The Segment list is the whole provenance API: there is no separate endpoint
// for "where did this bar come from?" — locate its open time in this list.
func (a *api) showComposite(w http.ResponseWriter, r *http.Request) {
	name, err := nameFromPath(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	view, err := a.svc.View(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asDetail(view))
}

// buildComposite answers POST /composites/{name}/build: reconcile the
// declaration against the source data that exists and leave the dataset ready
// or failed.
//
// A build that ran but could not leave the dataset ready — strict mode over an
// open Gap or a range the sources do not supply — is a 409 naming the reason;
// the dataset is then failed, and GET shows the Quality that explains it. A
// second concurrent Build of one dataset is a 409 too: builds must not
// interleave.
func (a *api) buildComposite(w http.ResponseWriter, r *http.Request) {
	name, err := nameFromPath(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	view, err := a.svc.Build(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asDetail(view))
}

// editComposite answers PUT /composites/{name}: replace the configuration.
// The body is a whole declaration, so what is stored is always what was sent.
// A dataset that has been built comes back stale.
func (a *api) editComposite(w http.ResponseWriter, r *http.Request) {
	name, err := nameFromPath(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	var body configJSON
	if err := decodeJSON(r, &body); err != nil {
		a.fail(w, err)
		return
	}
	cfg, err := body.toConfig()
	if err != nil {
		a.fail(w, err)
		return
	}
	edited, err := a.svc.Edit(r.Context(), name, cfg)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asComposite(edited))
}

// deleteComposite answers DELETE /composites/{name}: remove the declaration
// and everything derived from it. Source data is never touched. The body is
// the declaration as it stood, so a caller can see what it just removed.
func (a *api) deleteComposite(w http.ResponseWriter, r *http.Request) {
	name, err := nameFromPath(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	deleted, err := a.svc.Delete(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asComposite(deleted))
}
