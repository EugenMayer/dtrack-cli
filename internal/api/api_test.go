package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeactivateProject_AlreadyInactiveIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		// Dependency-Track returns 304 Not Modified when a PATCH wouldn't
		// change anything, e.g. deactivating an already-inactive project.
		w.WriteHeader(http.StatusNotModified)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL, "test-key")
	if err := client.DeactivateProject(context.Background(), "already-inactive"); err != nil {
		t.Fatalf("expected a 304 response to be treated as success, got error: %v", err)
	}
}

func TestDeactivateProject_OtherErrorsStillPropagate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL, "test-key")
	if err := client.DeactivateProject(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected a 404 to still be reported as an error")
	}
}
