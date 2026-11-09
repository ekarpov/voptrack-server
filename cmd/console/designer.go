package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The track designer: a researcher builds a study's tracks the way a person
// does in the app's composer -- a name, and values that are a number, a whole
// number, text, yes/no or a date and time, a number with a unit and whether
// it adds up or is a reading. Nobody is asked for a schema; "Show the schema"
// is there for whoever wants to see what the app will receive.
//
// No script runs in the Console, so the form carries blank rows: a filled
// blank value is added on save, a blank new track likewise, and a ticked
// Remove drops what it is on. One Save applies all of it.

const blankValues = 2

type designField struct {
	Index                     int
	Name, Kind, Unit, Meaning string
}

type designTrack struct {
	Index  int
	Name   string
	TypeID string
	Fields []designField
}

// designRows lays a study's tracks out as the form shows them, with blank
// rows to fill in and one blank track at the end.
func designRows(tracks []TrackDesign) []designTrack {
	var out []designTrack
	for _, t := range tracks {
		if t.Catalogue != "" {
			continue
		}
		row := designTrack{Index: len(out), Name: t.Name, TypeID: t.TypeID}
		for _, f := range t.Fields {
			row.Fields = append(row.Fields, designField{Index: len(row.Fields), Name: f.Name, Kind: string(f.Kind), Unit: f.Unit, Meaning: f.Meaning})
		}
		out = append(out, row)
	}
	out = append(out, designTrack{Index: len(out)})
	for i := range out {
		for b := 0; b < blankValues; b++ {
			out[i].Fields = append(out[i].Fields, designField{Index: len(out[i].Fields)})
		}
	}
	return out
}

// parseDesign reads the form back into tracks. Blank rows are ignored,
// removed rows dropped; identifiers are derived from names, which is safe
// while the study is a draft because nothing has been installed yet.
func parseDesign(form url.Values, study string) []TrackDesign {
	var out []TrackDesign
	for i := 0; form.Has(fmt.Sprintf("t%d.present", i)); i++ {
		p := fmt.Sprintf("t%d.", i)
		name := strings.TrimSpace(form.Get(p + "name"))
		t := TrackDesign{Name: name, TypeID: "study." + study + "." + identifier(name)}
		for j := 0; form.Has(fmt.Sprintf("%sf%d.present", p, j)); j++ {
			q := fmt.Sprintf("%sf%d.", p, j)
			fname := strings.TrimSpace(form.Get(q + "name"))
			if fname == "" || form.Get(q+"remove") != "" {
				continue
			}
			t.Fields = append(t.Fields, TrackField{ID: identifier(fname), Name: fname,
				Kind: FieldKind(form.Get(q + "kind")), Unit: strings.TrimSpace(form.Get(q + "unit")), Meaning: form.Get(q + "meaning")})
		}
		if form.Get(p+"remove") != "" || (name == "" && len(t.Fields) == 0) {
			continue
		}
		out = append(out, t)
	}
	for _, id := range form["catalogue"] {
		if _, ok := catalogue[id]; ok {
			out = append(out, TrackDesign{Catalogue: id})
		}
	}
	return out
}

func (a *app) tracksPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	st, found := a.studies.get(r.PathValue("slug"))
	if !found {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return
	}
	a.renderDesigner(w, r, sess, st, st.Tracks, r.URL.Query().Get("error"))
}

func (a *app) renderDesigner(w http.ResponseWriter, r *http.Request, sess *session, st Study, tracks []TrackDesign, problem string) {
	a.renderDesignerStatus(w, r, sess, st, tracks, problem, http.StatusOK, setupRevision(st))
}

func (a *app) renderDesignerStatus(w http.ResponseWriter, r *http.Request, sess *session, st Study, tracks []TrackDesign, problem string, status int, revision string) {
	picked := map[string]bool{}
	for _, t := range tracks {
		if t.Catalogue != "" {
			picked[t.Catalogue] = true
		}
	}
	schema := ""
	if types, err := studyTypes(tracks); err == nil {
		raw, _ := json.MarshalIndent(types, "", "  ")
		schema = string(raw)
	}
	a.render(w, r, sess, "study_tracks", status, map[string]any{
		"Study": st, "Rows": designRows(tracks), "Tracks": tracks, "Kinds": Kinds, "Catalogue": CatalogueTracks,
		"Picked": picked, "Schema": schema, "Locked": st.IssuerID != "" || (st.Setup != nil && st.Setup.Opening), "Error": problem, "Revision": revision})
}

func (a *app) saveTracks(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	st, found := a.studies.get(r.PathValue("slug"))
	if !found {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return
	}
	tracks := parseDesign(r.PostForm, st.ID())
	// A design that is not yet complete is still saved, so nothing typed is
	// lost; the study only refuses to open until it validates.
	saved, err := a.studies.saveTracks(st.Slug, tracks, r.PostFormValue("revision"))
	if err != nil {
		if err.Error() == "setup_stale" {
			a.renderDesignerStatus(w, r, sess, st, tracks, "setup_stale", http.StatusConflict, r.PostFormValue("revision"))
			return
		}
		a.renderDesigner(w, r, sess, st, tracks, err.Error())
		return
	}
	problem := ""
	if _, err := studyTypes(tracks); err != nil {
		problem = err.Error()
	}
	if problem != "" {
		a.renderDesigner(w, r, sess, saved, tracks, problem)
		return
	}
	http.Redirect(w, r, "/studies/"+st.Slug+"/tracks", http.StatusSeeOther)
}
