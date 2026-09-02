package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/example/dtrack-cli/internal/api"
)

type proj map[string]any

func mockServer(t *testing.T, deleted *[]string) *httptest.Server {
	t.Helper()
	collections := []proj{
		{"uuid": "col-1", "name": "Product A", "version": "prod", "collectionLogic": "AGGREGATE_DIRECT_CHILDREN", "active": true},
	}
	nonCollection := proj{"uuid": "leaf-x", "name": "Standalone", "version": "1.0", "active": true}
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
		items := []proj{}
		if page(r) == 1 {
			items = append(append(items, collections...), nonCollection)
		}
		writeJSON(w, len(collections)+1, items)
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
	mux.HandleFunc("/api/v1/project/batchDelete", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			UUIDs []string `json:"uuids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*deleted = body.UUIDs
		w.WriteHeader(http.StatusNoContent)
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
	opts := &cleanupOptions{includeInactive: true}

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
	opts := &cleanupOptions{
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
	opts := &cleanupOptions{collection: "Product A", revision: "9.9.9", yes: true, includeInactive: true}
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
	opts := &cleanupOptions{collection: "Product A", revision: "1.2.3", includeInactive: true}
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

func TestMatchNamedCollection_Ambiguous(t *testing.T) {
	cols := []api.Project{
		{UUID: "a", Name: "Dup", Version: "1"},
		{UUID: "b", Name: "Dup", Version: "2"},
	}
	if _, err := matchNamedCollection(cols, "Dup"); err == nil {
		t.Fatal("expected ambiguity error for duplicated name")
	}
	got, err := matchNamedCollection(cols, "Dup@2")
	if err != nil {
		t.Fatalf("unexpected error disambiguating: %v", err)
	}
	if got.UUID != "b" {
		t.Errorf("expected uuid b, got %s", got.UUID)
	}
}
