package commands

import (
	"context"
	"fmt"
	"io"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/spf13/cobra"
)

// cloneRunOptions holds the flags for "project clone" beyond the two
// positional arguments (source spec and new version).
type cloneRunOptions struct {
	clone      api.CloneOptions
	jsonOutput bool
	outputUUID bool
	noWait     bool
}

func newProjectCloneCmd(flags *rootFlags) *cobra.Command {
	opts := &cloneRunOptions{}

	cmd := &cobra.Command{
		Use:   "clone <name>[@source-version] <new-version>",
		Short: "Clone a project into a new version",
		Long: `Clone a project into a new version.

NAME identifies the source project to clone; append '@<version>' to
disambiguate projects that share a name across multiple versions.
NEW-VERSION is the version assigned to the cloned project.

By default the clone carries none of the source project's tags, properties,
dependencies, components, services, audit history, ACL, or policy
violations over — opt in with the corresponding --include-* flag. Cloning is
processed asynchronously by the Dependency-Track server: this command
reports the tracking token it returns, then polls until processing
completes and reports the resulting cloned project. Pass --no-wait to skip
polling and just report the token.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := flags.newClient()
			if err != nil {
				return err
			}
			return runClone(cmd.Context(), client, args[0], args[1], opts, cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.BoolVar(&opts.clone.IncludeTags, "include-tags", false, "Include tags in the clone.")
	f.BoolVar(&opts.clone.IncludeProperties, "include-properties", false, "Include properties in the clone.")
	f.BoolVar(&opts.clone.IncludeDependencies, "include-dependencies", false, "Include dependencies (BOM) in the clone.")
	f.BoolVar(&opts.clone.IncludeComponents, "include-components", false, "Include components in the clone.")
	f.BoolVar(&opts.clone.IncludeServices, "include-services", false, "Include services in the clone.")
	f.BoolVar(&opts.clone.IncludeAuditHistory, "include-audit-history", false,
		"Include audit history (findings analysis/suppressions) in the clone.")
	f.BoolVar(&opts.clone.IncludeACL, "include-acl", false, "Include the access control list in the clone.")
	f.BoolVar(&opts.clone.IncludePolicyViolations, "include-policy-violations", false, "Include policy violations in the clone.")
	f.BoolVar(&opts.clone.MakeCloneLatest, "make-clone-latest", false, "Mark the cloned project as the latest version.")
	f.BoolVar(&opts.jsonOutput, "json", false, "Print the result as JSON.")
	f.BoolVar(&opts.outputUUID, "output-uuid", false,
		"Print only the cloned project's uuid (or, with --no-wait, the tracking token), and nothing else.")
	f.BoolVar(&opts.noWait, "no-wait", false,
		"Report the tracking token and return immediately, without waiting for the clone to finish.")
	cmd.MarkFlagsMutuallyExclusive("json", "output-uuid")

	return cmd
}

// cloneResult is the JSON shape printed by "project clone --json". Project
// is populated once the clone has finished processing (nil with --no-wait).
type cloneResult struct {
	Token         string       `json:"token"`
	SourceUUID    string       `json:"sourceUuid"`
	SourceName    string       `json:"sourceName"`
	SourceVersion string       `json:"sourceVersion"`
	NewVersion    string       `json:"newVersion"`
	Project       *api.Project `json:"project,omitempty"`
}

// runClone resolves spec to a single source project, triggers the clone,
// and — unless opts.noWait — waits for it to finish processing and resolves
// the resulting cloned project (same name as the source, at newVersion) to
// report in place of the bare tracking token.
func runClone(ctx context.Context, client *api.Client, spec, newVersion string, opts *cloneRunOptions, out io.Writer) error {
	source, err := resolveProjectBySpec(ctx, client, spec)
	if err != nil {
		return err
	}

	token, err := client.CloneProject(ctx, source.UUID, newVersion, opts.clone)
	if err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}

	result := cloneResult{
		Token:         token,
		SourceUUID:    source.UUID,
		SourceName:    source.Name,
		SourceVersion: source.Version,
		NewVersion:    newVersion,
	}

	// --json/--output-uuid print exactly one line/value at the very end and
	// nothing else, so route the human-readable progress lines to a discard
	// writer while those flags are set.
	quiet := opts.jsonOutput || opts.outputUUID
	progress := out
	if quiet {
		progress = io.Discard
	}

	fmt.Fprintf(progress, "Cloning %s -> %s (source uuid: %s)\n", source.Label(), newVersion, source.UUID)
	fmt.Fprintf(progress, "Clone initiated. Token: %s\n", token)

	if opts.noWait {
		return reportCloneResult(out, opts, result)
	}

	if err := waitForTokenProcessing(ctx, client, token, "Clone is still being processed...", progress); err != nil {
		return err
	}

	cloned, err := client.LookupProject(ctx, source.Name, newVersion)
	if err != nil {
		return fmt.Errorf("clone finished but the resulting project could not be found: %w", err)
	}
	result.Project = &cloned

	fmt.Fprintf(progress, "Clone completed: %s (uuid: %s)\n", cloned.Label(), cloned.UUID)
	return reportCloneResult(out, opts, result)
}

// reportCloneResult prints the final, single-line result for --output-uuid
// or --json, or nothing further for the default human-readable mode (whose
// progress lines were already printed by runClone).
func reportCloneResult(out io.Writer, opts *cloneRunOptions, result cloneResult) error {
	if opts.outputUUID {
		if result.Project != nil {
			fmt.Fprintln(out, result.Project.UUID)
		} else {
			fmt.Fprintln(out, result.Token)
		}
		return nil
	}
	if opts.jsonOutput {
		return printJSON(out, result)
	}
	return nil
}
