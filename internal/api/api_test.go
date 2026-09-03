package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	dtrack "github.com/DependencyTrack/client-go"
)

// uuidSample is a syntactically valid, otherwise-meaningless project uuid
// used across this file's tests. dtrack.Project.UUID is a strictly-parsed
// uuid.UUID, so any uuid.Parse-able string works regardless of whether a
// real project exists behind it.
const uuidSample = "12345678-1234-1234-1234-123456789abc"

// writeVersion answers a GET /api/version request the way dtrack.NewClient
// expects: it's fetched eagerly, unauthenticated, on every client
// construction, so every mock server in this file needs to serve it.
func writeVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"version": "5.1.0"})
}

func TestDeactivateProject_AlreadyInactiveIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", writeVersion)
	mux.HandleFunc("PATCH /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		// Dependency-Track returns 304 Not Modified when a PATCH wouldn't
		// change anything, e.g. deactivating an already-inactive project.
		w.WriteHeader(http.StatusNotModified)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := New(srv.URL, "test-key")
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	if err := client.DeactivateProject(context.Background(), uuidSample); err != nil {
		t.Fatalf("expected a 304 response to be treated as success, got error: %v", err)
	}
}

func TestDeactivateProject_OtherErrorsStillPropagate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", writeVersion)
	mux.HandleFunc("PATCH /api/v1/project/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := New(srv.URL, "test-key")
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	if err := client.DeactivateProject(context.Background(), uuidSample); err == nil {
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
			// /api/version must succeed unauthenticated (as it does against a
			// real server) so client construction itself doesn't fail before
			// any of the calls under test get a chance to run.
			mux.HandleFunc("/api/version", writeVersion)
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			client, err := New(srv.URL, "test-key")
			if err != nil {
				t.Fatalf("building client: %v", err)
			}
			ctx := context.Background()

			calls := map[string]func() error{
				"GetProject": func() error {
					_, err := client.GetProject(ctx, uuidSample)
					return err
				},
				"ListCollectionProjects": func() error {
					_, err := client.ListCollectionProjects(ctx, false)
					return err
				},
				"ListProjectsByName": func() error {
					_, err := client.ListProjectsByName(ctx, "name", false)
					return err
				},
				"LookupProject": func() error {
					_, err := client.LookupProject(ctx, "name", "1.0")
					return err
				},
				"DeleteProject": func() error {
					return client.DeleteProject(ctx, uuidSample)
				},
				"DeactivateProject": func() error {
					return client.DeactivateProject(ctx, uuidSample)
				},
				"CloneProject": func() error {
					_, err := client.CloneProject(ctx, uuidSample, "2.0", CloneOptions{})
					return err
				},
				"UploadBOM": func() error {
					_, err := client.UploadBOM(ctx, "Zm9v", BOMUploadOptions{ProjectUUID: uuidSample})
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
					var apiErr *dtrack.APIError
					if !errors.As(err, &apiErr) {
						t.Fatalf("expected a *dtrack.APIError, got %T: %v", err, err)
					}
					if apiErr.StatusCode != status {
						t.Errorf("expected status %d, got %d", status, apiErr.StatusCode)
					}
				})
			}
		})
	}
}
