package guidedrecovery

import (
	rs "github.com/ekarpov/voptrack-server/internal/recoveryset"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Emits only synthetic templates for browser layout, focus and accessibility
// review. No real host authorization, credentials, records or input paths.
func TestUIReviewCorpus(t *testing.T) {
	dir := os.Getenv("VOPTRACK_GUIDED_UI_DIR")
	if dir == "" {
		t.Skip("optional synthetic UI corpus")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	s, _ := fixture(t)
	for _, lang := range languages {
		for _, phase := range []string{"public-error", "long-label-review", "enter", "select", "refused", "review", "accepted", "creating-target", "importing-kit", "recovering-producer", "restoring-records", "verifying", "complete", "stopped"} {
			v := &session{CSRF: "synthetic-csrf", Authorized: phase != "enter", Expires: time.Now().Add(time.Hour)}
			s.config.Sets[0].Label = "Synthetic Home"
			if phase == "long-label-review" {
				s.config.Sets[0].Label = strings.Repeat("x", 120)
				s.journal.Phase = "review"
			} else {
				s.journal.Phase = phase
			}
			s.journal.Set = "home"
			s.journal.Attempt = strings.Repeat("b", 48)
			s.journal.Binding = "synthetic-binding"
			s.journal.Checked = &rs.Receipt{Inputs: map[string]string{"backup": strings.Repeat("a", 64), "evidence": strings.Repeat("b", 64), "kit": strings.Repeat("c", 64), "settings": strings.Repeat("d", 64)}}
			w := httptest.NewRecorder()
			err := ""
			if phase == "refused" {
				err = tr(lang, "refused")
			}
			if phase == "public-error" {
				publicError(w, lang, 403)
			} else {
				s.render(w, lang, v, err)
			}
			if e := os.WriteFile(filepath.Join(dir, lang+"-"+phase+".html"), w.Body.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
}
