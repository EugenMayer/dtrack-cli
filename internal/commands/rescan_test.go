package commands

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRescan_AllActiveChildrenByDefault(t *testing.T) {
	var analyzed []string
	srv := mockServerWithAnalyze(t, &analyzed, nil)
	defer srv.Close()

	var out strings.Builder
	// Pick collection #1, confirm "y" — no version prompt for rescan.
	in := strings.NewReader("1\ny\n")
	opts := &childrenActionOptions{}

	if err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, in, &out); err != nil {
		t.Fatalf("runRescan returned error: %v\noutput:\n%s", err, out.String())
	}

	// Inactive worker (c4) is excluded by default; every active child is
	// rescanned regardless of version.
	want := []string{uuidChild1, uuidChild2, uuidChild3}
	if !slices.Equal(analyzed, want) {
		t.Errorf("expected %v to be analyzed, got %v", want, analyzed)
	}
	if strings.Contains(out.String(), "Enter the child project version") {
		t.Errorf("rescan must not prompt for a version:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Rescanned 3 project(s).") {
		t.Errorf("expected rescan summary in output:\n%s", out.String())
	}
}

func TestRescan_VersionFilterIncludeInactive(t *testing.T) {
	var analyzed []string
	srv := mockServerWithAnalyze(t, &analyzed, nil)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", revision: "1.2.3", includeInactive: true, yes: true}
	if err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out.String())
	}

	want := []string{uuidChild1, uuidChild2, uuidChild4}
	if !slices.Equal(analyzed, want) {
		t.Errorf("expected %v to be analyzed, got %v", want, analyzed)
	}
}

func TestRescan_DryRun(t *testing.T) {
	var analyzed []string
	srv := mockServerWithAnalyze(t, &analyzed, nil)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A@prod", dryRun: true}
	if err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analyzed != nil {
		t.Errorf("dry run must not trigger any analysis; got %v", analyzed)
	}
	if !strings.Contains(out.String(), "Dry run: no projects were rescanned.") {
		t.Errorf("expected dry-run notice in output:\n%s", out.String())
	}
}

func TestRescan_AbortOnNo(t *testing.T) {
	var analyzed []string
	srv := mockServerWithAnalyze(t, &analyzed, nil)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A"}
	if err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader("n\n"), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analyzed != nil {
		t.Errorf("abort must trigger nothing; got %v", analyzed)
	}
	if !strings.Contains(out.String(), "Aborted. Nothing was rescanned.") {
		t.Errorf("expected abort message:\n%s", out.String())
	}
}

func TestRescan_WaitsForProcessing(t *testing.T) {
	withFastPolling(t, time.Millisecond, time.Second)
	var analyzed []string
	tokens := &cloneCapture{processingSequence: []bool{true, true, false}}
	srv := mockServerWithAnalyze(t, &analyzed, tokens)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", revision: "1.2.4", yes: true}
	if err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out.String())
	}
	if tokens.pollCount != 3 {
		t.Errorf("expected 3 polls, got %d", tokens.pollCount)
	}
	if !strings.Contains(out.String(), "Analysis of frontend 1.2.4 is still running...") ||
		!strings.Contains(out.String(), "Analysis of frontend 1.2.4 completed.") {
		t.Errorf("expected progress/completion messages:\n%s", out.String())
	}
}

func TestRescan_NoWaitSkipsPolling(t *testing.T) {
	var analyzed []string
	tokens := &cloneCapture{processingSequence: []bool{true}}
	srv := mockServerWithAnalyze(t, &analyzed, tokens)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", yes: true, noWait: true}
	if err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens.pollCount != 0 {
		t.Errorf("--no-wait must not poll; got %d polls", tokens.pollCount)
	}
	if len(analyzed) != 3 {
		t.Errorf("expected 3 analyses triggered, got %v", analyzed)
	}
	if !strings.Contains(out.String(), "Triggered analysis of 3 project(s); not waiting for completion.") {
		t.Errorf("expected no-wait summary:\n%s", out.String())
	}
}

func TestRescan_TimesOutIfNeverFinishes(t *testing.T) {
	withFastPolling(t, time.Millisecond, 20*time.Millisecond)
	tokens := &cloneCapture{processingSequence: []bool{true}}
	srv := mockServerWithAnalyze(t, nil, tokens)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", yes: true}
	err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got %v", err)
	}
}

func TestRescan_AlreadyRunningAnalysisIsSkipped(t *testing.T) {
	withFastPolling(t, time.Millisecond, time.Second)
	analysisRunningUUID = uuidChild2
	t.Cleanup(func() { analysisRunningUUID = "" })

	var analyzed []string
	tokens := &cloneCapture{}
	srv := mockServerWithAnalyze(t, &analyzed, tokens)
	defer srv.Close()

	var out strings.Builder
	opts := &childrenActionOptions{collection: "Product A", yes: true}
	if err := runRescan(context.Background(), newTestClient(t, srv.URL), opts, strings.NewReader(""), &out); err != nil {
		t.Fatalf("a 409 for one child must not fail the rescan: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Analysis of backend 1.2.3 is already in progress") {
		t.Errorf("expected an already-in-progress notice:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Analysis of backend 1.2.3 completed.") {
		t.Errorf("must not wait on an analysis it didn't trigger:\n%s", out.String())
	}
	// Only the two triggered analyses are polled.
	if tokens.pollCount != 2 {
		t.Errorf("expected 2 polls, got %d", tokens.pollCount)
	}
}
