package main

import (
	"errors"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"net/http"
)

// The operation lock spans durable intent, external creation and local issuer
// persistence. On an ambiguous response the intent stays: types cannot change.
// Tessera alpha.33 lists issuer IDs and supports rotating a lost secret, but
// does not return full installed schemas. A pre-existing ID without our saved
// intent is therefore an operator dependency, never adopted or overwritten.
func (a *app) provisionStudyIssuer(r *http.Request, token string, st Study, policy IssuerPolicy) error {
	var list struct {
		Issuers []struct {
			ID string `json:"id"`
		} `json:"issuers"`
	}
	if err := a.tessera.call(r.Context(), "GET", "/admin/api/v1/issuers", token, "", nil, 200, &list); err != nil {
		return errors.New("setup_recovery")
	}
	exists := false
	for _, v := range list.Issuers {
		if v.ID == policy.ID {
			exists = true
		}
	}
	draft := setupDraft(st)
	if exists && !draft.Opening {
		return errors.New("setup_recovery")
	}
	if !draft.Opening {
		draft.Opening = true
		draft.Stage = "review"
		st.Setup = &draft
		if err := a.studies.putSetup(st); err != nil {
			return errors.New("setup_unavailable")
		}
	}
	return recoverymaterial.Change(a.cfg.StateDir, "issuer:"+st.Slug, func() error {
		var secret string
		var err error
		if exists {
			secret, err = a.tessera.rotateIssuerSecret(r.Context(), token, policy.ID)
		} else {
			secret, err = a.tessera.createIssuer(r.Context(), token, policy)
		}
		if err != nil || secret == "" {
			return errors.New("setup_recovery")
		}
		return a.studies.setIssuer(st.Slug, policy.ID, secret, studyCap)
	})
}
