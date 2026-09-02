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

// cloneCapture records the PUT /v1/project/clone request body a test makes,
// and lets the test override the "token" returned in the response.
type cloneCapture struct {
	request map[string]any
	token   string // response token; defaults to "clone-token" when empty
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
		{"uuid": "col-1", "name": "Product A", "version": "prod", "collectionLogic": "AGGREGATE_DIRECT_CHILDREN", "active": true},
	}
	nonCollection := proj{"uuid": "leaf-x", "name": "Standalone", "version": "1.0", "active": true}
	searchTargets := []proj{
		{"uuid": "s1", "name": "search-me", "version": "1.0.0", "active": true},
		{"uuid": "s2", "name": "search-me", "version": "2.0.0", "active": false},
	}
	children := []proj{
		{"uuid": "c1", "name": "frontend", "version": "1.2.3", "active": true},
		{"uuid": "c2", "name": "backend", "version": "1.2.3", "active": true},
		{"uuid": "c3", "name": "frontend", "version": "1.2.4", "active": true},
		{"uuid": "c4", "name": "worker", "version": "1.2.3", "active": false},
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
		writeJSON(w, 1, proj{"version": "5.1.0"})
	})
	mux.HandleFunc("/api/v1/project", func(w http.ResponseWriter, r *http.Request) {
		// Mirrors the real server: "name" is an exact match, applied
		// alongside excludeInactive.
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
	mux.HandleFunc("/api/v1/project/col-1/children", func(w http.ResponseWriter, r *http.Request) {
		excl := r.URL.Query().Get("excludeInactive") == "true"
		var data []proj
		for _, c := range children {
			if c["active"].(bool) || !excl {
				data = append(data, c)
			}
		}
		items := []proj{}
		if page(r) == 1 {
			items = data
		}
		writeJSON(w, len(data), items)
	})
	mux.HandleFunc("POST /api/v1/project/batchDelete", func(w http.ResponseWriter, r *http.Request) {
		// Dependency-Track expects a bare JSON array of UUID strings, which it
		// deserializes into a Set<UUID>. Rejecting an object here mirrors the
		// real server's HTTP 400 and guards against regressing to a wrapper.
		var uuids []string
		if err := json.NewDecoder(r.Body).Decode(&uuids); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		*deleted = uuids
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PATCH /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		// Partial update: Dependency-Track deserializes the body straight into
		// a Project and merges only the fields present, so a bare
		// {"active": false} payload is enough to deactivate.
		var body map[string]bool
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if deactivated != nil {
			*deactivated = append(*deactivated, r.PathValue("uuid"))
		}
		writeJSON(w, 1, proj{"uuid": r.PathValue("uuid"), "active": body["active"]})
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

	return httptest.NewServer(mux)
}

func newTestClient(url string) *api.Client {
	return api.New(url, "test-key")
}

func TestCleanup_Interactive(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	// Pick collection #1, version 1.2.3, confirm "y".
	in := strings.NewReader("1\n1.2.3\ny\n")
	opts := &childrenActionOptions{includeInactive: true}

	if err := runCleanup(context.Background(), newTestClient(srv.URL), opts, in, &out); err != nil {
		t.Fatalf("runCleanup returned error: %v\noutput:\n%s", err, out.String())
	}

	got := map[string]bool{}
	for _, u := range deleted {
		got[u] = true
	}
	// Inactive worker (c4) is included by default; c1, c2, c4 match 1.2.3.
	for _, want := range []string{"c1", "c2", "c4"} {
		if !got[want] {
			t.Errorf("expected %s to be deleted; got %v", want, deleted)
		}
	}
	if got["c3"] {
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
	if err := runCleanup(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out); err != nil {
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
	if err := runCleanup(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out); err != nil {
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
	if err := runCleanup(context.Background(), newTestClient(srv.URL), opts, strings.NewReader("n\n"), &out); err != nil {
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

	if err := runDeactivate(context.Background(), newTestClient(srv.URL), opts, in, &out); err != nil {
		t.Fatalf("runDeactivate returned error: %v\noutput:\n%s", err, out.String())
	}

	if deleted != nil {
		t.Errorf("deactivate must never call batchDelete; got %v", deleted)
	}

	got := map[string]bool{}
	for _, u := range deactivated {
		got[u] = true
	}
	// Inactive worker (c4) is included by default; c1, c2, c4 match 1.2.3.
	for _, want := range []string{"c1", "c2", "c4"} {
		if !got[want] {
			t.Errorf("expected %s to be deactivated; got %v", want, deactivated)
		}
	}
	if got["c3"] {
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
	if err := runDeactivate(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out); err != nil {
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
	if err := runDeactivate(context.Background(), newTestClient(srv.URL), opts, strings.NewReader("n\n"), &out); err != nil {
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
	if err := runSearch(context.Background(), newTestClient(srv.URL), opts, &out); err != nil {
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
	if err := runSearch(context.Background(), newTestClient(srv.URL), opts, &out); err != nil {
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
	if err := runSearch(context.Background(), newTestClient(srv.URL), opts, &out); err != nil {
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
	if err := runSearch(context.Background(), newTestClient(srv.URL), opts, &out); err != nil {
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
	if err := runSearch(context.Background(), newTestClient(srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []api.Project
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("expected valid JSON output, got error %v:\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].UUID != "s1" {
		t.Errorf("expected a single project s1, got %+v", got)
	}
}

func TestSearch_OutputUUID(t *testing.T) {
	var deleted []string
	srv := mockServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &searchOptions{name: "search-me", outputUUID: true}
	if err := runSearch(context.Background(), newTestClient(srv.URL), opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.Fields(out.String())
	want := map[string]bool{"s1": true, "s2": true}
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
