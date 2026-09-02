package commands

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bomCapture records the PUT /v1/bom request body a test makes, lets the
// test override the returned token, and scripts a sequence of "processing"
// values returned by successive GET /v1/event/token/{uuid} polls (the last
// value repeats once the sequence is exhausted).
type bomCapture struct {
	request            map[string]any
	token              string // defaults to "bom-token" when empty
	processingSequence []bool
	pollCount          int
}

func mockBomServer(t *testing.T, capture *bomCapture) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/bom", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token := "bom-token"
		if capture != nil {
			capture.request = body
			if capture.token != "" {
				token = capture.token
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
	})
	mux.HandleFunc("GET /api/v1/event/token/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		processing := false
		if capture != nil {
			if capture.pollCount < len(capture.processingSequence) {
				processing = capture.processingSequence[capture.pollCount]
			} else if len(capture.processingSequence) > 0 {
				processing = capture.processingSequence[len(capture.processingSequence)-1]
			}
			capture.pollCount++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"processing": processing})
	})
	return httptest.NewServer(mux)
}

// withFastBomPolling shortens the package-level poll interval/timeout for
// the duration of a test and restores them afterward.
func withFastBomPolling(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	origInterval, origTimeout := bomPollInterval, bomPollTimeout
	bomPollInterval, bomPollTimeout = interval, timeout
	t.Cleanup(func() { bomPollInterval, bomPollTimeout = origInterval, origTimeout })
}

func writeTempBom(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bom.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp bom file: %v", err)
	}
	return path
}

func TestBomUpload_ByUUID_WaitsForProcessing(t *testing.T) {
	withFastBomPolling(t, time.Millisecond, time.Second)
	capture := &bomCapture{processingSequence: []bool{true, false}}
	srv := mockBomServer(t, capture)
	defer srv.Close()

	bomPath := writeTempBom(t, `{"bomFormat":"CycloneDX"}`)
	var out strings.Builder
	opts := &bomUploadOptions{byUUID: "proj-uuid"}
	if err := runBomUpload(context.Background(), newTestClient(srv.URL), bomPath, opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capture.request["project"] != "proj-uuid" {
		t.Errorf("expected project=proj-uuid in request, got %v", capture.request["project"])
	}
	gotBom, _ := capture.request["bom"].(string)
	decoded, err := base64.StdEncoding.DecodeString(gotBom)
	if err != nil || string(decoded) != `{"bomFormat":"CycloneDX"}` {
		t.Errorf("expected the bom field to be the base64-encoded file content, got %q (err %v)", gotBom, err)
	}

	if !strings.Contains(out.String(), "Token: bom-token") {
		t.Errorf("expected the token in output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "still being processed") {
		t.Errorf("expected at least one processing update:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "BOM processing completed.") {
		t.Errorf("expected a completion message:\n%s", out.String())
	}
}

func TestBomUpload_ByNameAutoCreateWithParentAndIsLatest(t *testing.T) {
	withFastBomPolling(t, time.Millisecond, time.Second)
	capture := &bomCapture{processingSequence: []bool{false}}
	srv := mockBomServer(t, capture)
	defer srv.Close()

	bomPath := writeTempBom(t, "<bom/>")
	var out strings.Builder
	opts := &bomUploadOptions{
		name:          "New Project",
		version:       "1.0.0",
		autoCreate:    true,
		parentName:    "Parent",
		parentVersion: "1.0.0",
		isLatest:      true,
	}
	if err := runBomUpload(context.Background(), newTestClient(srv.URL), bomPath, opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := map[string]any{
		"projectName":    "New Project",
		"projectVersion": "1.0.0",
		"autoCreate":     true,
		"parentName":     "Parent",
		"parentVersion":  "1.0.0",
		"isLatest":       true,
	}
	for field, want := range checks {
		if got := capture.request[field]; got != want {
			t.Errorf("expected %s=%v in request, got %v", field, want, got)
		}
	}
	if _, present := capture.request["project"]; present {
		t.Errorf("expected no 'project' field when identifying by name, got %v", capture.request["project"])
	}
}

func TestBomUpload_NoWaitSkipsPolling(t *testing.T) {
	capture := &bomCapture{processingSequence: []bool{true, true, true}}
	srv := mockBomServer(t, capture)
	defer srv.Close()

	bomPath := writeTempBom(t, "{}")
	var out strings.Builder
	opts := &bomUploadOptions{byUUID: "proj-uuid", noWait: true}
	if err := runBomUpload(context.Background(), newTestClient(srv.URL), bomPath, opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capture.pollCount != 0 {
		t.Errorf("expected no polling with --no-wait, got %d poll(s)", capture.pollCount)
	}
	if strings.Contains(out.String(), "processing") {
		t.Errorf("expected no processing messages with --no-wait:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Token: bom-token") {
		t.Errorf("expected the token in output:\n%s", out.String())
	}
}

func TestBomUpload_MissingIdentification(t *testing.T) {
	srv := mockBomServer(t, nil)
	defer srv.Close()

	bomPath := writeTempBom(t, "{}")
	var out strings.Builder
	opts := &bomUploadOptions{}
	err := runBomUpload(context.Background(), newTestClient(srv.URL), bomPath, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "--by-uuid or --name") {
		t.Fatalf("expected an identification error, got: %v", err)
	}
}

func TestBomUpload_ByUUIDWithAutoCreateRejected(t *testing.T) {
	srv := mockBomServer(t, nil)
	defer srv.Close()

	bomPath := writeTempBom(t, "{}")
	var out strings.Builder
	opts := &bomUploadOptions{byUUID: "proj-uuid", autoCreate: true}
	err := runBomUpload(context.Background(), newTestClient(srv.URL), bomPath, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "--by-uuid") {
		t.Fatalf("expected --auto-create with --by-uuid to be rejected, got: %v", err)
	}
}

func TestBomUpload_TimesOutIfNeverFinishes(t *testing.T) {
	withFastBomPolling(t, time.Millisecond, 10*time.Millisecond)
	capture := &bomCapture{processingSequence: []bool{true}}
	srv := mockBomServer(t, capture)
	defer srv.Close()

	bomPath := writeTempBom(t, "{}")
	var out strings.Builder
	opts := &bomUploadOptions{byUUID: "proj-uuid"}
	err := runBomUpload(context.Background(), newTestClient(srv.URL), bomPath, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got: %v", err)
	}
}
