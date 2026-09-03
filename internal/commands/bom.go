package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/spf13/cobra"
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
	byUUID         string
	name           string
	version        string
	autoCreate     bool
	parentName     string
	parentVersion  string
	parentUUID     string
	isLatest       bool
	noWait         bool
	skipIfInactive bool
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

With --skip-if-inactive, the project is looked up first: if it already
exists and is inactive, the upload is skipped with a warning (exit code 0)
instead of uploading to it.

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
	f.BoolVar(&opts.skipIfInactive, "skip-if-inactive", false,
		"If the project already exists and is inactive, skip the upload with a warning instead of uploading to it.")
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

	if opts.skipIfInactive {
		skip, err := shouldSkipInactiveProject(ctx, client, byUUID, name, strings.TrimSpace(opts.version), opts.autoCreate, out)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
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
	if err := waitForTokenProcessing(ctx, client, token, "BOM is still being processed...", out); err != nil {
		return err
	}
	fmt.Fprintln(out, "BOM processing completed.")
	return nil
}

// shouldSkipInactiveProject looks up the target project (by uuid or by
// name/version) and reports whether the upload should be skipped because it
// already exists and is inactive, printing a warning to out when it does.
//
// A "not found" lookup is only an error when the project isn't going to be
// auto-created: nothing to skip for a project that doesn't exist yet, since
// auto-create always makes new projects active.
func shouldSkipInactiveProject(ctx context.Context, client *api.Client, byUUID, name, version string, autoCreate bool, out io.Writer) (bool, error) {
	var (
		target api.Project
		err    error
	)
	if byUUID != "" {
		target, err = client.GetProject(ctx, byUUID)
	} else {
		target, err = client.LookupProject(ctx, name, version)
	}
	if err != nil {
		if byUUID == "" && autoCreate && api.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking whether the project is inactive: %w", err)
	}

	if !target.Active {
		fmt.Fprintf(out, "Warning: project %s (uuid: %s) is inactive; skipping BOM upload.\n", target.Label(), target.UUID)
		return true, nil
	}
	return false, nil
}
