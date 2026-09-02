package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockDeleteServer serves GET /v1/project/{uuid}, GET /v1/project/lookup,
// and DELETE /v1/project/{uuid} for a single known project (uuid "p1"),
// recording every deleted uuid into capture.
func mockDeleteServer(t *testing.T, capture *[]string) *httptest.Server {
	t.Helper()
	project := proj{"uuid": "p1", "name": "Product A", "version": "1.0.0", "active": true}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/project/lookup", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != project["name"] || r.URL.Query().Get("version") != project["version"] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(project)
	})
	mux.HandleFunc("GET /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("uuid") != project["uuid"] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(project)
	})
	mux.HandleFunc("DELETE /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		*capture = append(*capture, r.PathValue("uuid"))
		w.WriteHeader(http.StatusNoContent)
	})
	return httptest.NewServer(mux)
}

func TestProjectDelete_ByUUID_Confirmed(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &projectDeleteOptions{byUUID: "p1"}
	if err := runProjectDelete(context.Background(), newTestClient(srv.URL), opts, strings.NewReader("y\n"), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "p1" {
		t.Errorf("expected p1 to be deleted, got %v", deleted)
	}
	if !strings.Contains(out.String(), "Deleted project Product A 1.0.0 (uuid: p1)") {
		t.Errorf("expected a deletion summary:\n%s", out.String())
	}
}

func TestProjectDelete_ByNameVersion_Yes(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &projectDeleteOptions{projectName: "Product A", version: "1.0.0", yes: true}
	if err := runProjectDelete(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "p1" {
		t.Errorf("expected p1 to be deleted, got %v", deleted)
	}
}

func TestProjectDelete_AbortOnNo(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &projectDeleteOptions{byUUID: "p1"}
	if err := runProjectDelete(context.Background(), newTestClient(srv.URL), opts, strings.NewReader("n\n"), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("abort must delete nothing; got %v", deleted)
	}
	if !strings.Contains(out.String(), "Aborted. Nothing was deleted.") {
		t.Errorf("expected an abort message:\n%s", out.String())
	}
}

func TestProjectDelete_MissingIdentification(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &projectDeleteOptions{}
	err := runProjectDelete(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "--by-uuid or --project-name/--version") {
		t.Fatalf("expected an identification error, got: %v", err)
	}
}

func TestProjectDelete_NameWithoutVersion(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &projectDeleteOptions{projectName: "Product A"}
	err := runProjectDelete(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "--version is required") {
		t.Fatalf("expected a missing-version error, got: %v", err)
	}
}

func TestProjectDelete_LookupNotFound(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &projectDeleteOptions{projectName: "Does Not Exist", version: "1.0.0", yes: true}
	err := runProjectDelete(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("expected an error for a project that does not exist")
	}
	if len(deleted) != 0 {
		t.Errorf("nothing should be deleted on a failed lookup; got %v", deleted)
	}
}

func TestProjectDelete_ByUUIDNotFound(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &projectDeleteOptions{byUUID: "does-not-exist", yes: true}
	err := runProjectDelete(context.Background(), newTestClient(srv.URL), opts, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("expected an error for a uuid that does not exist")
	}
	if len(deleted) != 0 {
		t.Errorf("nothing should be deleted on a failed lookup; got %v", deleted)
	}
}
