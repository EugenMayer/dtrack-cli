package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/eugenmayer/dtrack-cli/internal/api"
)

// printJSON writes v to out as indented JSON, terminated by a newline.
func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// pollInterval and pollTimeout govern how commands wait for the server to
// finish processing an async job (BOM upload, project clone) started via a
// tracking token. Tests override these package vars to keep polling fast.
var (
	pollInterval = 2 * time.Second
	pollTimeout  = 5 * time.Minute
)

// waitForTokenProcessing polls token's processing status, via Dependency-
// Track's generic event-token endpoint, until it completes, pollTimeout
// elapses, or ctx is cancelled. inProgressMsg is printed to out before each
// wait; the caller prints its own completion message once this returns nil.
func waitForTokenProcessing(ctx context.Context, client *api.Client, token, inProgressMsg string, out io.Writer) error {
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	for {
		processing, err := client.IsTokenProcessing(ctx, token)
		if err != nil {
			return err
		}
		if !processing {
			return nil
		}
		fmt.Fprintln(out, inProgressMsg)

		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for processing to finish (token %s); check status manually", token)
		case <-time.After(pollInterval):
		}
	}
}
