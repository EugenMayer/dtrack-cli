package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/spf13/cobra"
)

// bomPollInterval and bomPollTimeout govern how "project bom upload" waits
// for the server to finish processing an uploaded BOM. Tests override these
// package vars to keep polling fast.
var (
	bomPollInterval = 2 * time.Second
	bomPollTimeout  = 5 * time.Minute
)

// newBomCmd builds the "project bom" group and its subcommands.
func newBomCmd(flags *rootFlags) *cobra.Command {
	bomCmd := &cobra.Command{
		Use:   "bom",
		Short: "Commands for working with a project's BOM",
	}
	bomCmd.AddCommand(newBomUploadCmd(flags))
	return bomCmd
}

// bomUploadOptions holds the flags for "project bom upload".
type bomUploadOptions struct {
	byUUID        string
	name          string
	version       string
	autoCreate    bool
	parentName    string
	parentVersion string
	parentUUID    string
	isLatest      bool
	noWait        bool
}

func newBomUploadCmd(flags *rootFlags) *cobra.Command {
	opts := &bomUploadOptions{}

	cmd := &cobra.Command{
		Use:   "upload <bom-file>",
		Short: "Upload a CycloneDX BOM to a project",
		Long: `Upload a CycloneDX BOM (JSON or XML) to a project.

The target project is identified either by name (--name, optionally with
--version) or, if given, directly by --by-uuid — the two are mutually
exclusive. With --name and no matching project, pass --auto-create to have
Dependency-Track create it (optionally under a parent identified by
--parent-name/--parent-version or --parent-uuid).

Dependency-Track processes the upload asynchronously: this command reports
the tracking token and, unless --no-wait is given, polls until processing
completes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := flags.newClient()
			if err != nil {
				return err
			}
			return runBomUpload(cmd.Context(), client, args[0], opts, cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.byUUID, "by-uuid", "", "Identify the project directly by UUID, instead of --name/--version.")
	f.StringVar(&opts.name, "name", "", "Project name to upload to (or auto-create).")
	f.StringVar(&opts.version, "version", "", "Project version to upload to (used with --name).")
	f.BoolVar(&opts.autoCreate, "auto-create", false,
		"Create the project named by --name/--version if it does not already exist.")
	f.StringVar(&opts.parentName, "parent-name", "", "Parent project name, used when auto-creating.")
	f.StringVar(&opts.parentVersion, "parent-version", "", "Parent project version, used when auto-creating.")
	f.StringVar(&opts.parentUUID, "parent-uuid", "", "Parent project UUID, used when auto-creating.")
	f.BoolVar(&opts.isLatest, "is-latest", false, "Mark the uploaded BOM as belonging to the latest version of the project.")
	f.BoolVar(&opts.noWait, "no-wait", false,
		"Report the tracking token and return immediately, without waiting for processing to finish.")
	cmd.MarkFlagsMutuallyExclusive("by-uuid", "name")

	return cmd
}

// runBomUpload reads bomPath, uploads it for the project identified by opts,
// and (unless opts.noWait) waits for the server to finish processing it.
func runBomUpload(ctx context.Context, client *api.Client, bomPath string, opts *bomUploadOptions, out io.Writer) error {
	byUUID := strings.TrimSpace(opts.byUUID)
	name := strings.TrimSpace(opts.name)
	if byUUID == "" && name == "" {
		return fmt.Errorf("the project must be identified with either --by-uuid or --name")
	}
	if byUUID != "" && (opts.autoCreate || opts.parentName != "" || opts.parentVersion != "" || opts.parentUUID != "") {
		return fmt.Errorf("--auto-create and --parent-* only apply when identifying the project by --name, not --by-uuid")
	}

	data, err := os.ReadFile(bomPath)
	if err != nil {
		return fmt.Errorf("reading BOM file: %w", err)
	}
	bomBase64 := base64.StdEncoding.EncodeToString(data)

	uploadOpts := api.BOMUploadOptions{
		ProjectUUID:   byUUID,
		Name:          name,
		Version:       strings.TrimSpace(opts.version),
		AutoCreate:    opts.autoCreate,
		ParentName:    strings.TrimSpace(opts.parentName),
		ParentVersion: strings.TrimSpace(opts.parentVersion),
		ParentUUID:    strings.TrimSpace(opts.parentUUID),
		IsLatest:      opts.isLatest,
	}

	token, err := client.UploadBOM(ctx, bomBase64, uploadOpts)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	fmt.Fprintf(out, "BOM uploaded successfully. Token: %s\n", token)

	if opts.noWait {
		return nil
	}
	return waitForBomProcessing(ctx, client, token, out)
}

// waitForBomProcessing polls the token's processing status until it
// completes, bomPollTimeout elapses, or ctx is cancelled.
func waitForBomProcessing(ctx context.Context, client *api.Client, token string, out io.Writer) error {
	pollCtx, cancel := context.WithTimeout(ctx, bomPollTimeout)
	defer cancel()

	for {
		processing, err := client.IsBOMProcessing(ctx, token)
		if err != nil {
			return err
		}
		if !processing {
			fmt.Fprintln(out, "BOM processing completed.")
			return nil
		}
		fmt.Fprintln(out, "BOM is still being processed...")

		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for BOM processing to finish (token %s); check status manually", token)
		case <-time.After(bomPollInterval):
		}
	}
}
