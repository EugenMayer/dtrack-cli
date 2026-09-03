package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/eugenmayer/dtrack-cli/internal/api"
)

type proj map[string]any

// Fixture UUIDs used across this file's mock server. dtrack.Project.UUID is
// a strictly-parsed uuid.UUID, so every "uuid" field the mock returns (and
// every uuid a test passes as a --by-uuid-style argument) must be a
// syntactically valid UUID — a short mnemonic like "c1" will fail to decode.
const (
	uuidCollection1 = "11111111-1111-1111-1111-111111111111" // col-1: the "Product A" collection
	uuidLeafX       = "22222222-2222-2222-2222-222222222222" // leaf-x: a non-collection, non-child project
	uuidSearch1     = "33333333-3333-3333-3333-333333333333" // s1: search-me 1.0.0 (active)
	uuidSearch2     = "44444444-4444-4444-4444-444444444444" // s2: search-me 2.0.0 (inactive)
	uuidChild1      = "55555555-5555-5555-5555-555555555555" // c1: frontend 1.2.3 (active)
	uuidChild2      = "66666666-6666-6666-6666-666666666666" // c2: backend 1.2.3 (active)
	uuidChild3      = "77777777-7777-7777-7777-777777777777" // c3: frontend 1.2.4 (active)
	uuidChild4      = "88888888-8888-8888-8888-888888888888" // c4: worker 1.2.3 (inactive)
	uuidClonedProj  = "99999999-9999-9999-9999-999999999999" // cloned-1: result of a clone
	uuidUnknown     = "ffffffff-ffff-ffff-ffff-ffffffffffff" // valid, but never a known project
)

// cloneCapture records the PUT /v1/project/clone request body a test makes,
// lets the test override the "token" returned in the response, and scripts
// how the async job that clone kicks off behaves:
//   - processingSequence is returned by successive GET /v1/event/token/{uuid}
//     polls, in order (the last value repeats once exhausted; a nil/empty
//     sequence means "done immediately").
//   - clonedProject, when set, is what GET /v1/project/lookup returns for a
//     name+version match, simulating the finished cloned project.
type cloneCapture struct {
	request            map[string]any
	token              string // response token; defaults to "clone-token" when empty
	processingSequence []bool
	pollCount          int
	clonedProject      proj
}

func mockServer(t *testing.T, deleted *[]string) *httptest.Server {
	return mockServerFull(t, deleted, nil, nil)
}

// mockServerWithDeactivate is mockServer plus a PATCH handler that records
// every uuid deactivated (in call order, duplicates included) into
// *deactivated. Pass a nil deactivated to skip that behaviour.
func mockServerWithDeactivate(t *testing.T, deleted *[]string, deactivated *[]string) *httptest.Server {
	return mockServerFull(t, deleted, deactivated, nil)
}

// mockServerWithClone is mockServer plus a PUT /clone handler that records
// the request into clone and returns clone.token (or "clone-token" if unset).
func mockServerWithClone(t *testing.T, clone *cloneCapture) *httptest.Server {
	var deleted []string
	return mockServerFull(t, &deleted, nil, clone)
}

func mockServerFull(t *testing.T, deleted *[]string, deactivated *[]string, clone *cloneCapture) *httptest.Server {
	t.Helper()
	collections := []proj{
		{"uuid": uuidCollection1, "name": "Product A", "version": "prod", "collectionLogic": "AGGREGATE_DIRECT_CHILDREN", "active": true},
	}
	nonCollection := proj{"uuid": uuidLeafX, "name": "Standalone", "version": "1.0", "active": true}
	searchTargets := []proj{
		{"uuid": uuidSearch1, "name": "search-me", "version": "1.0.0", "active": true},
		{"uuid": uuidSearch2, "name": "search-me", "version": "2.0.0", "active": false},
	}
	children := []proj{
		{"uuid": uuidChild1, "name": "frontend", "version": "1.2.3", "active": true},
		{"uuid": uuidChild2, "name": "backend", "version": "1.2.3", "active": true},
		{"uuid": uuidChild3, "name": "frontend", "version": "1.2.4", "active": true},
		{"uuid": uuidChild4, "name": "worker", "version": "1.2.3", "active": false},
	}

	// allProjects is every project the mock server knows about: the fixed
	// pool above, plus the simulated post-clone result once one exists.
	allProjects := func() []proj {
		all := append(append(append(append([]proj{}, collections...), nonCollection), searchTargets...), children...)
		if clone != nil && clone.clonedProject != nil {
			all = append(all, clone.clonedProject)
		}
		return all
	}

	// activeState tracks each child's current "active" flag so the PATCH
	// handler below can mirror Dependency-Track's real behavior: a PATCH
	// that doesn't actually change anything returns 304 Not Modified rather
	// than 200.
	activeState := map[string]bool{}
	for _, c := range children {
		activeState[c["uuid"].(string)] = c["active"].(bool)
	}

	writeJSON := func(w http.ResponseWriter, total int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		_ = json.NewEncoder(w).Encode(v)
	}
	page := func(r *http.Request) int {
		if p := r.URL.Query().Get("pageNumber"); p != "" {
			n, _ := strconv.Atoi(p)
			return n
		}
		return 1
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) {
		// dtrack.NewClient fetches this eagerly (unauthenticated) to learn
		// the server version; the client-go About struct's other fields are
		// left absent here, which decodes fine as their zero values.
		writeJSON(w, 1, proj{"version": "5.1.0"})
	})
	mux.HandleFunc("/api/v1/project", func(w http.ResponseWriter, r *http.Request) {
		// Mirrors the real server: "name" is an exact match, applied
		// alongside excludeInactive. GetAll (used for the collection/search
		// listing) sends neither, so this only actually filters requests
		// coming from GetProjectsForName.
		name := r.URL.Query().Get("name")
		excl := r.URL.Query().Get("excludeInactive") == "true"

		all := append(append(append([]proj{}, collections...), nonCollection), searchTargets...)
		var filtered []proj
		for _, p := range all {
			if name != "" && p["name"].(string) != name {
				continue
			}
			if excl && !p["active"].(bool) {
				continue
			}
			filtered = append(filtered, p)
		}

		items := []proj{}
		if page(r) == 1 {
			items = filtered
		}
		writeJSON(w, len(filtered), items)
	})
	mux.HandleFunc("/api/v1/project/"+uuidCollection1+"/children", func(w http.ResponseWriter, r *http.Request) {
		// client-go's GetChildren has no excludeInactive param, so the real
		// client always fetches everything and filters client-side; this
		// handler ignores the query param for the same reason.
		items := []proj{}
		if page(r) == 1 {
			items = children
		}
		writeJSON(w, len(children), items)
	})
	mux.HandleFunc("POST /api/v1/project/batchDelete", func(w http.ResponseWriter, r *http.Request) {
		// No longer called by the client (client-go has no batchDelete
		// wrapper; BatchDelete loops per-project deletes instead), but kept
		// registered so a regression back to it would be caught by
		// TestCleanup_Interactive asserting on *deleted instead of silently
		// passing.
		var uuids []string
		if err := json.NewDecoder(r.Body).Decode(&uuids); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		*deleted = uuids
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		*deleted = append(*deleted, r.PathValue("uuid"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PATCH /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		// Partial update: Dependency-Track deserializes the body straight into
		// a Project and merges only the fields it cares about. client-go's
		// Project struct lacks omitempty on a few fields (metrics,
		// lastBomImport), so the body isn't just {"active": false} — decode
		// as map[string]any and only look at "active", ignoring the rest, the
		// same way the real server's patch handler does. Mirrors the real
		// server's "nothing changed" behavior too: if the requested value
		// matches the project's current state, respond 304 instead of 200.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if deactivated != nil {
			*deactivated = append(*deactivated, r.PathValue("uuid"))
		}
		uuid := r.PathValue("uuid")
		wantActiveRaw, hasActive := body["active"]
		wantActive, _ := wantActiveRaw.(bool)
		if hasActive && activeState[uuid] == wantActive {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		activeState[uuid] = wantActive
		writeJSON(w, 1, proj{"uuid": uuid, "active": wantActive})
	})
	mux.HandleFunc("PUT /api/v1/project/clone", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token := "clone-token"
		if clone != nil {
			clone.request = body
			if clone.token != "" {
				token = clone.token
			}
		}
		writeJSON(w, 1, proj{"token": token})
	})
	mux.HandleFunc("GET /api/v1/event/token/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		processing := false
		if clone != nil {
			if clone.pollCount < len(clone.processingSequence) {
				processing = clone.processingSequence[clone.pollCount]
			} else if len(clone.processingSequence) > 0 {
				processing = clone.processingSequence[len(clone.processingSequence)-1]
			}
			clone.pollCount++
		}
		writeJSON(w, 1, proj{"processing": processing})
	})
	mux.HandleFunc("GET /api/v1/project/lookup", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		version := r.URL.Query().Get("version")
		for _, p := range allProjects() {
			if p["name"] == name && p["version"] == version {
				writeJSON(w, 1, p)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		uuid := r.PathValue("uuid")
		for _, p := range allProjects() {
			if p["uuid"] == uuid {
				writeJSON(w, 1, p)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	return httptest.NewServer(mux)
}

// newTestClient builds an api.Client against a mock server, failing the test
// immediately if construction fails (e.g. the mock doesn't serve
// GET /api/version, which dtrack.NewClient always fetches eagerly).
func newTestClient(t *testing.T, url string) *api.Client {
	t.Helper()
	client, err := api.New(url, "test-key")
	if err != nil {
		t.Fatalf("building test client: %v", err)
	}
	return client
}

func TestCleanup_Interactive(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	// Pick collection #1, version 1.2.3, confirm "y".
	in := strings.NewReader("1\n1.2.3\ny\n")
	opts := &childrenActionOptions{includeInactive: true}

	if err := runCleanup(context.Background(), newTestClient(t, srv.URL), opts, in, &out); err != nil {
		t.Fatalf("runCleanup returned error: %v\noutput:\n%s", err, out.String())
	}

	got := map[string]bool{}
	for _, u := range deleted {
		got[u] = true
	}
	// Inactive worker (c4) is included by default; c1, c2, c4 match 1.2.3.
	for _, want := range []string{uuidChild1, uuidChild2, uuidChild4} {
		if !got[want] {
			t.Errorf("expected %s to be deleted; got %v", want, deleted)
		}
	}
	if got[uuidChild3] {
		t.Errorf("c3 (version 1.2.4) should not be deleted")
	}
	if len(deleted) != 3 {
		t.Errorf("expected 3 deletions, got %d (%v)", len(deleted), deleted)
	}
}

func TestCleanup_DryRunExcludeInactiveNamed(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{
		collection:      "Product A@prod",
		revision:        "1.2.3",
		includeInactive: false,
		dryRun:          true,
	}
	if err := runCleanup(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != nil {
		t.Errorf("dry run must not delete anything; got %v", deleted)
	}
	if !strings.Contains(out.String(), "Dry run") {
		t.Errorf("expected dry-run notice in output:\n%s", out.String())
	}
	// With inactive excluded, only c1 and c2 should appear in the overview.
	if strings.Contains(out.String(), "worker") {
		t.Errorf("inactive worker should be excluded from overview:\n%s", out.String())
	}
}

func TestCleanup_NoMatches(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", revision: "9.9.9", yes: true, includeInactive: true}
	if err := runCleanup(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != nil {
		t.Errorf("no matches should delete nothing; got %v", deleted)
	}
	if !strings.Contains(out.String(), "Nothing to do") {
		t.Errorf("expected 'Nothing to do' message:\n%s", out.String())
	}
}

func TestCleanup_AbortOnNo(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", revision: "1.2.3", includeInactive: true}
	// Answer "n" at the confirmation prompt.
	if err := runCleanup(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader("n\n"), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != nil {
		t.Errorf("abort must delete nothing; got %v", deleted)
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("expected abort message:\n%s", out.String())
	}
}

func TestDeactivate_Interactive(t *testing.T) {
	var deleted, deactivated []string
	srv := mockServerWithDeactivate(t, &deleted, &deactivated)
	defer srv.Close()

	var out strings.Builder
	// Pick collection #1, version 1.2.3, confirm "y".
	in := strings.NewReader("1\n1.2.3\ny\n")
	opts := &childrenActionOptions{includeInactive: true}

	// c4 (worker) is already inactive, so deactivating it is a no-op on the
	// server: Dependency-Track responds 304 Not Modified rather than 200,
	// which must not surface as an error.
	if err := runDeactivate(context.Background(), newTestClient(t, srv.URL), opts, in, &out); err != nil {
		t.Fatalf("runDeactivate returned error: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Deactivated 3 project(s).") {
		t.Errorf("expected a success summary despite the already-inactive project:\n%s", out.String())
	}

	if deleted != nil {
		t.Errorf("deactivate must never call batchDelete; got %v", deleted)
	}

	got := map[string]bool{}
	for _, u := range deactivated {
		got[u] = true
	}
	// Inactive worker (c4) is included by default; c1, c2, c4 match 1.2.3.
	for _, want := range []string{uuidChild1, uuidChild2, uuidChild4} {
		if !got[want] {
			t.Errorf("expected %s to be deactivated; got %v", want, deactivated)
		}
	}
	if got[uuidChild3] {
		t.Errorf("c3 (version 1.2.4) should not be deactivated")
	}
	if len(deactivated) != 3 {
		t.Errorf("expected 3 deactivations, got %d (%v)", len(deactivated), deactivated)
	}
	if !strings.Contains(out.String(), "Deactivated 3 project(s).") {
		t.Errorf("expected deactivation summary in output:\n%s", out.String())
	}
}

func TestDeactivate_DryRun(t *testing.T) {
	var deleted, deactivated []string
	srv := mockServerWithDeactivate(t, &deleted, &deactivated)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{
		collection:      "Product A@prod",
		revision:        "1.2.3",
		includeInactive: true,
		dryRun:          true,
	}
	if err := runDeactivate(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deactivated != nil {
		t.Errorf("dry run must not deactivate anything; got %v", deactivated)
	}
	if !strings.Contains(out.String(), "Dry run: no projects were deactivated.") {
		t.Errorf("expected dry-run notice in output:\n%s", out.String())
	}
}

func TestDeactivate_AbortOnNo(t *testing.T) {
	var deleted, deactivated []string
	srv := mockServerWithDeactivate(t, &deleted, &deactivated)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", revision: "1.2.3", includeInactive: true}
	// Answer "n" at the confirmation prompt.
	if err := runDeactivate(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader("n\n"), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deactivated != nil {
		t.Errorf("abort must deactivate nothing; got %v", deactivated)
	}
	if !strings.Contains(out.String(), "Aborted. Nothing was deactivated.") {
		t.Errorf("expected abort message:\n%s", out.String())
	}
}

func TestSearch_AllVersions(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &searchOptions{name: "search-me"}
	if err := runSearch(context.Background(), newTestClient(t, srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "2 project(s) found") {
		t.Errorf("expected both versions to be found:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1.0.0") || !strings.Contains(out.String(), "2.0.0") {
		t.Errorf("expected both versions listed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[inactive]") {
		t.Errorf("expected the inactive version to be flagged:\n%s", out.String())
	}
}

func TestSearch_OnlyActive(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &searchOptions{name: "search-me", onlyActive: true}
	if err := runSearch(context.Background(), newTestClient(t, srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "1 project(s) found") {
		t.Errorf("expected only the active version to be found:\n%s", out.String())
	}
	if strings.Contains(out.String(), "2.0.0") {
		t.Errorf("inactive version 2.0.0 should be excluded:\n%s", out.String())
	}
}

func TestSearch_VersionFilter(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &searchOptions{name: "search-me", version: "2.0.0"}
	if err := runSearch(context.Background(), newTestClient(t, srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "1 project(s) found") {
		t.Errorf("expected exactly one match for version 2.0.0:\n%s", out.String())
	}
	if strings.Contains(out.String(), "1.0.0") {
		t.Errorf("version 1.0.0 should be filtered out:\n%s", out.String())
	}
}

func TestSearch_NoMatches(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &searchOptions{name: "does-not-exist"}
	if err := runSearch(context.Background(), newTestClient(t, srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "No projects found") {
		t.Errorf("expected a no-matches message:\n%s", out.String())
	}
}

func TestSearch_JSON(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &searchOptions{name: "search-me", version: "1.0.0", jsonOutput: true}
	if err := runSearch(context.Background(), newTestClient(t, srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []api.Project
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("expected valid JSON output, got error %v:\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].UUID != uuidSearch1 {
		t.Errorf("expected a single project %s, got %+v", uuidSearch1, got)
	}
}

func TestSearch_OutputUUID(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &searchOptions{name: "search-me", outputUUID: true}
	if err := runSearch(context.Background(), newTestClient(t, srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.Fields(out.String())
	want := map[string]bool{uuidSearch1: true, uuidSearch2: true}
	if len(got) != 2 {
		t.Fatalf("expected 2 uuids, got %v", got)
	}
	for _, u := range got {
		if !want[u] {
			t.Errorf("unexpected uuid %q in output %v", u, got)
		}
	}
}

func TestMatchNamedProject_Ambiguous(t *testing.T) {
	cols := []api.Project{
		{UUID: "a", Name: "Dup", Version: "1"},
		{UUID: "b", Name: "Dup", Version: "2"},
	}
	if _, err := matchNamedProject(cols, "Dup"); err == nil {
		t.Fatal("expected ambiguity error for duplicated name")
	}
	got, err := matchNamedProject(cols, "Dup@2")
	if err != nil {
		t.Fatalf("unexpected error disambiguating: %v", err)
	}
	if got.UUID != "b" {
		t.Errorf("expected uuid b, got %s", got.UUID)
	}
}
