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
processed asynchronously by the Dependency-Track server, so this command
reports the tracking token it returns, not the finished project.`,
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
		"Print only the clone's tracking token/uuid, and nothing else.")
	cmd.MarkFlagsMutuallyExclusive("json", "output-uuid")

	return cmd
}

// cloneResult is the JSON shape printed by "project clone --json".
type cloneResult struct {
	Token         string `json:"token"`
	SourceUUID    string `json:"sourceUuid"`
	SourceName    string `json:"sourceName"`
	SourceVersion string `json:"sourceVersion"`
	NewVersion    string `json:"newVersion"`
}

// runClone resolves spec to a single source project, triggers the clone, and
// reports the tracking token Dependency-Track returns for the async job.
func runClone(ctx context.Context, client *api.Client, spec, newVersion string, opts *cloneRunOptions, out io.Writer) error {
	source, err := resolveProjectBySpec(ctx, client, spec)
	if err != nil {
		return err
	}

	token, err := client.CloneProject(ctx, source.UUID, newVersion, opts.clone)
	if err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}

	if opts.outputUUID {
		fmt.Fprintln(out, token)
		return nil
	}

	if opts.jsonOutput {
		return printJSON(out, cloneResult{
			Token:         token,
			SourceUUID:    source.UUID,
			SourceName:    source.Name,
			SourceVersion: source.Version,
			NewVersion:    newVersion,
		})
	}

	fmt.Fprintf(out, "Cloning %s -> %s (source uuid: %s)\n", source.Label(), newVersion, source.UUID)
	fmt.Fprintf(out, "Clone initiated. Token: %s\n", token)
	return nil
}
