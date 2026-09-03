package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eugenmayer/dtrack-cli/internal/api"
)

func TestGet_Default(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &getOptions{}
	if err := runGet(context.Background(), newTestClient(t, srv.URL), uuidP1, opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Product A", "1.0.0", uuidP1} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected %q in output:\n%s", want, out.String())
		}
	}
}

func TestGet_JSON(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &getOptions{jsonOutput: true}
	if err := runGet(context.Background(), newTestClient(t, srv.URL), uuidP1, opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got api.Project
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("expected valid JSON output, got error %v:\n%s", err, out.String())
	}
	if got.UUID != uuidP1 || got.Name != "Product A" || got.Version != "1.0.0" {
		t.Errorf("unexpected project in JSON output: %+v", got)
	}
}

func TestGet_OutputUUID(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &getOptions{outputUUID: true}
	if err := runGet(context.Background(), newTestClient(t, srv.URL), uuidP1, opts, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != uuidP1 {
		t.Errorf("expected output-uuid to print only %s, got %q", uuidP1, got)
	}
}

func TestGet_NotFound(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &getOptions{}
	err := runGet(context.Background(), newTestClient(t, srv.URL), uuidUnknown, opts, &out)
	if err == nil {
		t.Fatal("expected an error for an unknown uuid")
	}
}

func TestGet_EmptyUUID(t *testing.T) {
	var deleted []string
	srv := mockDeleteServer(t, &deleted)
	defer srv.Close()

	var out strings.Builder
	opts := &getOptions{}
	err := runGet(context.Background(), newTestClient(t, srv.URL), "   ", opts, &out)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected an empty-uuid error, got: %v", err)
	}
}
