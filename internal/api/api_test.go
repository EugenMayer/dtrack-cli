package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestAuthFailuresPropagateAsErrors locks in that every client method
// surfaces a non-nil error on HTTP 401/403, across every verb the client
// uses (GET, PUT, PATCH, DELETE). Every command's RunE ultimately calls one
// of these methods, and cmd/dtrack's main() exits 1 on any non-nil error, so
// this is what guarantees "401/403 -> exit code 1" for every command.
func TestAuthFailuresPropagateAsErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprintf("HTTP_%d", status), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			client := New(srv.URL, "test-key")
			ctx := context.Background()

			calls := map[string]func() error{
				"GetProject": func() error {
					_, err := client.GetProject(ctx, "some-uuid")
					return err
				},
				"ListProjects": func() error {
					_, err := client.ListProjects(ctx, false, false)
					return err
				},
				"LookupProject": func() error {
					_, err := client.LookupProject(ctx, "name", "1.0")
					return err
				},
				"DeleteProject": func() error {
					return client.DeleteProject(ctx, "some-uuid")
				},
				"DeactivateProject": func() error {
					return client.DeactivateProject(ctx, "some-uuid")
				},
				"CloneProject": func() error {
					_, err := client.CloneProject(ctx, "src-uuid", "2.0", CloneOptions{})
					return err
				},
				"UploadBOM": func() error {
					_, err := client.UploadBOM(ctx, "Zm9v", BOMUploadOptions{ProjectUUID: "some-uuid"})
					return err
				},
				"IsTokenProcessing": func() error {
					_, err := client.IsTokenProcessing(ctx, "some-token")
					return err
				},
			}

			for name, call := range calls {
				t.Run(name, func(t *testing.T) {
					err := call()
					if err == nil {
						t.Fatalf("expected an error for HTTP %d, got nil", status)
					}
					if !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
						t.Errorf("expected the error to mention HTTP %d, got: %v", status, err)
					}
				})
			}
		})
	}
}
