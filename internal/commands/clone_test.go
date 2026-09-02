package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/eugenmayer/dtrack-cli/internal/api"
)

func TestClone_Basic(t *testing.T) {
	capture := cloneCapture{
		clonedProject: proj{"uuid": "cloned-1", "name": "search-me", "version": "3.0.0", "active": true},
	}
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{clone: api.CloneOptions{IncludeComponents: true, MakeCloneLatest: true}}
	if err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capture.request["project"] != "s1" {
		t.Errorf("expected clone request for source uuid s1, got %v", capture.request["project"])
	}
	if capture.request["version"] != "3.0.0" {
		t.Errorf("expected new version 3.0.0 in request, got %v", capture.request["version"])
	}
	if capture.request["includeComponents"] != true {
		t.Errorf("expected includeComponents=true in request, got %v", capture.request)
	}
	if capture.request["makeCloneLatest"] != true {
		t.Errorf("expected makeCloneLatest=true in request, got %v", capture.request)
	}
	if capture.request["includeTags"] != false {
		t.Errorf("expected includeTags to default to false, got %v", capture.request)
	}
	if !strings.Contains(out.String(), "Token: clone-token") {
		t.Errorf("expected the tracking token in output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Clone completed: search-me 3.0.0 (uuid: cloned-1)") {
		t.Errorf("expected the resolved cloned project in output:\n%s", out.String())
	}
}

func TestClone_AmbiguousSource(t *testing.T) {
	var capture cloneCapture
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{}
	err := runClone(context.Background(), newTestClient(srv.URL), "search-me", "3.0.0", opts, &out)
	if err == nil {
		t.Fatal("expected an ambiguity error when the source name matches multiple versions")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected an ambiguity error, got: %v", err)
	}
	if capture.request != nil {
		t.Errorf("clone should not be called when the source is ambiguous; got request %v", capture.request)
	}
}

func TestClone_UnknownSource(t *testing.T) {
	srv := mockServerWithClone(t, nil)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{}
	err := runClone(context.Background(), newTestClient(srv.URL), "does-not-exist", "3.0.0", opts, &out)
	if err == nil {
		t.Fatal("expected an error for an unknown source project")
	}
}

func TestClone_WaitsForProcessingThenResolvesProject(t *testing.T) {
	withFastPolling(t, time.Millisecond, time.Second)
	capture := cloneCapture{
		processingSequence: []bool{true, true, false},
		clonedProject:      proj{"uuid": "cloned-1", "name": "search-me", "version": "3.0.0", "active": true},
	}
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{}
	if err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Clone is still being processed...") {
		t.Errorf("expected at least one processing update:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Clone completed: search-me 3.0.0 (uuid: cloned-1)") {
		t.Errorf("expected the resolved cloned project in output:\n%s", out.String())
	}
}

func TestClone_NoWaitSkipsPollingAndResolution(t *testing.T) {
	capture := cloneCapture{processingSequence: []bool{true, true, true}}
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{noWait: true}
	if err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.pollCount != 0 {
		t.Errorf("expected no polling with --no-wait, got %d poll(s)", capture.pollCount)
	}
	if strings.Contains(out.String(), "Clone completed") {
		t.Errorf("expected no resolved-project message with --no-wait:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Token: clone-token") {
		t.Errorf("expected the tracking token in output:\n%s", out.String())
	}
}

func TestClone_TimesOutIfNeverFinishes(t *testing.T) {
	withFastPolling(t, time.Millisecond, 10*time.Millisecond)
	capture := cloneCapture{processingSequence: []bool{true}}
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{}
	err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got: %v", err)
	}
}

func TestClone_OutputUUID(t *testing.T) {
	capture := cloneCapture{
		clonedProject: proj{"uuid": "cloned-1", "name": "search-me", "version": "3.0.0", "active": true},
	}
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{outputUUID: true}
	if err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "cloned-1" {
		t.Errorf("expected output-uuid to print only the cloned project's uuid, got %q", got)
	}
}

func TestClone_OutputUUID_NoWaitPrintsToken(t *testing.T) {
	capture := cloneCapture{token: "11111111-2222-3333-4444-555555555555"}
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{outputUUID: true, noWait: true}
	if err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != capture.token {
		t.Errorf("expected output-uuid --no-wait to print only the token %q, got %q", capture.token, got)
	}
}

func TestClone_JSON(t *testing.T) {
	capture := cloneCapture{
		clonedProject: proj{"uuid": "cloned-1", "name": "search-me", "version": "3.0.0", "active": true},
	}
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{jsonOutput: true}
	if err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got cloneResult
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("expected valid JSON output, got error %v:\n%s", err, out.String())
	}
	if got.Token != "clone-token" {
		t.Errorf("expected token clone-token, got %q", got.Token)
	}
	if got.SourceUUID != "s1" {
		t.Errorf("expected source uuid s1, got %q", got.SourceUUID)
	}
	if got.NewVersion != "3.0.0" {
		t.Errorf("expected new version 3.0.0, got %q", got.NewVersion)
	}
	if got.Project == nil || got.Project.UUID != "cloned-1" {
		t.Errorf("expected the resolved cloned project in JSON output, got %+v", got.Project)
	}
}

func TestClone_JSON_NoWaitOmitsProject(t *testing.T) {
	var capture cloneCapture
	srv := mockServerWithClone(t, &capture)
	defer srv.Close()

	var out strings.Builder
	opts := &cloneRunOptions{jsonOutput: true, noWait: true}
	if err := runClone(context.Background(), newTestClient(srv.URL), "search-me@1.0.0", "3.0.0", opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got cloneResult
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("expected valid JSON output, got error %v:\n%s", err, out.String())
	}
	if got.Project != nil {
		t.Errorf("expected no project in JSON output with --no-wait, got %+v", got.Project)
	}
}
