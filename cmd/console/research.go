package main

import (
	"context"
	"fmt"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// A study's dashboards read every participant's store through the
// study-analysis service, which each study's issuer names as a reader. Which
// stores belong to the study comes from the consent records (who joined it)
// matched against the administration API's overview (which store each of those
// accounts holds) -- never from store names, which a researcher could rename.
//
// They are the researcher's alone. Study-specific suppression makes aggregate
// presentation safer; participant administration and raw exports retain access.

const studyScopePrefix = "study:"

func studySlug(scope string) (string, bool) {
	if !strings.HasPrefix(scope, studyScopePrefix) {
		return "", false
	}
	slug := strings.TrimPrefix(scope, studyScopePrefix)
	return slug, validSlug.MatchString(slug)
}

func (a *app) studySources(ctx context.Context, sess *session, slug string) []Source {
	sources, _ := a.studySourceDiscovery(ctx, sess, slug, false)
	return sources
}

func (a *app) discoverStudySources(ctx context.Context, sess *session, slug string) ([]Source, error) {
	return a.studySourceDiscovery(ctx, sess, slug, true)
}

func (a *app) studySourceDiscovery(ctx context.Context, sess *session, slug string, strict bool) ([]Source, error) {
	if sess == nil || sess.Owner == nil || a.cfg.Analysis == "" {
		return nil, fmt.Errorf("study sources unavailable")
	}
	token, err := sess.Owner.token(ctx, a.tessera)
	if err != nil {
		return nil, fmt.Errorf("study sources unavailable")
	}
	held, err := a.tessera.heldBy(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("study sources unavailable")
	}
	seen := map[string]bool{}
	withdrawn := a.studies.withdrawals(slug)
	// Each participant's cohort is the consent version they agreed to last.
	cohort := map[string]string{}
	for _, c := range a.studies.consents(slug) {
		cohort[c.Account] = fmt.Sprintf("v%d", c.Version)
	}
	// What the study's reader still holds. A participant who withdrew keeps
	// it frozen (Tessera alpha.28): what they sent before stays with the
	// study, as their consent agreed, and nothing after it is visible. One
	// who erased with Remove my data holds a replacement it does not read.
	analysisReads := map[string]bool{}
	for _, d := range held[a.cfg.Analysis] {
		analysisReads[d] = true
	}
	var out []Source
	for _, c := range a.studies.consents(slug) {
		if seen[c.Account] {
			continue
		}
		seen[c.Account] = true
		stores := held[c.Account]
		if strict && len(stores) > 1 {
			return nil, fmt.Errorf("ambiguous participant stores")
		}
		if len(stores) != 1 {
			continue // not yet redeemed, or deleted: nothing to read
		}
		if _, gone := withdrawn[c.Account]; gone && !analysisReads[stores[0]] {
			continue // erased: nothing of theirs is the study's any more
		}
		dataset, account := stores[0], c.Account
		out = append(out, Source{Dataset: dataset, Name: account, Participant: account, Cohort: cohort[account],
			Token:  func(ctx context.Context) (string, error) { return a.analysisCache.token(ctx, a.tessera, dataset) },
			Forget: func() { a.analysisCache.forget(dataset) }})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// studyDashboard is a study's overview, for its researcher.
func (a *app) studyDashboard(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	if _, found := a.studies.get(r.PathValue("slug")); !found {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return
	}
	st, _ := a.studies.get(r.PathValue("slug"))
	a.monitorPage(w, r, sess, st, false)
}

// ParticipantState is what the researcher's list says about a person: taking
// part, withdrawn (with the date when they withdrew through the Console),
// suspended by the researcher, or gone (the account was deleted). A store emptied from the app, not the
// Console, is withdrawn without a date (see erasedFromApp).
type ParticipantState struct {
	Key string
	At  time.Time
}

func (a *app) participantStates(ctx context.Context, sess *session, slug string) map[string]ParticipantState {
	out := map[string]ParticipantState{}
	withdrawn := a.studies.withdrawals(slug)
	var held map[string][]string
	suspended := map[string]bool{}
	if token, err := sess.Owner.token(ctx, a.tessera); err != nil {
		log.Printf("console: participant list: owner sign-in: %v", err)
	} else if accounts, err := a.tessera.accounts(ctx, token); err != nil {
		log.Printf("console: participant list: who holds which store: %v", err)
	} else {
		held = map[string][]string{}
		for _, acc := range accounts {
			suspended[acc.Account] = acc.Suspended
			held[acc.Account] = []string{} // present, even holding nothing
			for d := range acc.Datasets {
				held[acc.Account] = append(held[acc.Account], d)
			}
		}
	}
	for _, c := range a.studies.consents(slug) {
		if _, done := out[c.Account]; done {
			continue
		}
		if at, ok := withdrawn[c.Account]; ok {
			out[c.Account] = ParticipantState{Key: "state_withdrawn", At: at}
			continue
		}
		if held == nil {
			out[c.Account] = ParticipantState{Key: "state_unknown"}
			continue
		}
		stores, exists := held[c.Account]
		if !exists {
			out[c.Account] = ParticipantState{Key: "state_gone"}
			continue
		}
		state := ParticipantState{Key: "state_active"}
		if suspended[c.Account] {
			state.Key = "state_suspended"
		}
		if a.cfg.Analysis != "" && erasedFromApp(held[a.cfg.Analysis], stores, func(dataset string) ([]Meta, error) {
			token, err := a.analysisCache.token(ctx, a.tessera, dataset)
			if err != nil {
				return nil, err
			}
			return a.tessera.types(ctx, token, dataset)
		}) {
			state = ParticipantState{Key: "state_withdrawn"}
		}
		out[c.Account] = state
	}
	return out
}

// participantAction suspends or reinstates a participant: their app stops
// reaching their store at once, and nothing in it is touched. Only someone who
// consented to this study can be acted on from its page.
func (a *app) participantAction(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	slug, account, action := r.PathValue("slug"), r.PathValue("account"), r.PathValue("action")
	if action != "suspend" && action != "reinstate" {
		a.fail(w, r, sess, http.StatusNotFound, "error", nil)
		return
	}
	consented := false
	for _, c := range a.studies.consents(slug) {
		consented = consented || c.Account == account
	}
	if !consented {
		a.fail(w, r, sess, http.StatusNotFound, "error", nil)
		return
	}
	token, err := sess.Owner.token(r.Context(), a.tessera)
	if err == nil {
		err = a.tessera.accountAction(r.Context(), token, account, action)
	}
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	http.Redirect(w, r, "/studies/"+slug+"/participants", http.StatusSeeOther)
}

// erasedFromApp reports whether a participant's store was erased outside the
// Console. Tessera gives an erased store a new identity that only the eraser
// holds, so the study's analysis reader holding none of the participant's
// stores is the sign; that reader was added to every store the issuer made.
// A store the reader does hold but that has no tracks left is the same event
// seen on a server that does not move holds on erasure: participants cannot
// change schemas, so only erasing empties one.
func erasedFromApp(analysisHolds, participantHolds []string, types func(dataset string) ([]Meta, error)) bool {
	reads := map[string]bool{}
	for _, d := range analysisHolds {
		reads[d] = true
	}
	for _, d := range participantHolds {
		if !reads[d] {
			continue
		}
		list, err := types(d)
		if err != nil || len(list) > 0 {
			return false // still read, with tracks, or unknown: not withdrawn
		}
	}
	return true // no store of theirs is read with tracks left
}

// replaceLostSecrets gives every study whose issuer secret is missing or whose
// rotation is unresolved a new one. A completed local write does not prove that
// an interrupted operation cleared its durable pending marker. A secret is a
// file, never a Console document, so a server restored onto
// an empty Console volume has its studies back and none of their secrets;
// without this, nobody could join them. Rotating needs the researcher's server
// rights, so it happens when they sign in.
func (a *app) replaceLostSecrets(ctx context.Context, ownerToken string) {
	a.studies.operation.Lock()
	defer a.studies.operation.Unlock()
	for _, st := range a.studies.list() {
		if st.IssuerID == "" {
			continue
		}
		operation := "issuer:" + st.Slug
		err := recoverymaterial.ChangeIf(a.cfg.StateDir, operation, func(state recoverymaterial.State) bool {
			_, err := a.studies.issuerSecret(st.Slug)
			return err != nil || state.Pending[operation]
		}, func() error {
			secret, err := a.tessera.rotateIssuerSecret(ctx, ownerToken, st.IssuerID)
			if err == nil {
				err = a.studies.writeSecret(st.Slug, secret)
			}
			return err
		})
		if err != nil {
			log.Printf("console: replacing a study's lost issuer secret failed")
		}
	}
}

// completeness is, per participant, how many records each study track holds
// and when the latest of them happened: who is recording and who has stopped.
// One count per participant and track, read through the analysis reader as
// the dashboard reads.
type completeness struct {
	Counts map[string]map[string]string // pseudonym -> track -> records
	Last   map[string]string            // pseudonym -> latest timestamp, as recorded
}

// Reading completeness is two requests per participant and track, so it runs
// a few at a time under one deadline: a large study shows what was read in
// time and says the rest was not, instead of holding the page.
const (
	completenessWorkers  = 8
	completenessDeadline = 15 * time.Second
)

func (a *app) studyCompleteness(ctx context.Context, sess *session, st Study) completeness {
	out := completeness{Counts: map[string]map[string]string{}, Last: map[string]string{}}
	types, err := studyTracks(st.Tracks)
	if err != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, completenessDeadline)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, completenessWorkers)
	for _, src := range a.studySources(ctx, sess, st.Slug) {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			token, err := src.Token(ctx)
			if err != nil {
				return
			}
			counts, last := map[string]string{}, ""
			for _, t := range types {
				groups, err := a.tessera.aggregate(ctx, token, src.Dataset, t.ID, "", "", nil)
				if err != nil || len(groups) == 0 {
					continue
				}
				counts[t.ID] = groups[0].RowCount.String()
				if l, err := a.tessera.latest(ctx, token, src.Dataset, t.ID, "timestamp"); err == nil && l > last {
					last = l // timestamps in one form compare as text
				}
			}
			mu.Lock()
			out.Counts[src.Name], out.Last[src.Name] = counts, last
			mu.Unlock()
		}(src)
	}
	wg.Wait()
	return out
}

// enrolment counts participants by status, for the head of the list.
func enrolment(states map[string]ParticipantState) map[string]int {
	out := map[string]int{}
	for _, s := range states {
		out[s.Key]++
	}
	return out
}
