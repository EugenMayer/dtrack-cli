package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/spf13/cobra"
)

// projectDeleteOptions holds the flags for "project delete".
type projectDeleteOptions struct {
	byUUID      string
	projectName string
	version     string
	yes         bool
}

func newProjectDeleteCmd(flags *rootFlags) *cobra.Command {
	opts := &projectDeleteOptions{}

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a single project",
		Long: `Delete a single project.

The project is identified either directly by --by-uuid, or by
--project-name together with --version (mutually exclusive with
--by-uuid). This permanently deletes the project and everything under it
(components, findings, children, ...); there is no undo.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := flags.newClient()
			if err != nil {
				return err
			}
			return runProjectDelete(cmd.Context(), client, opts, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.byUUID, "by-uuid", "", "Identify the project directly by UUID, instead of --project-name/--version.")
	f.StringVar(&opts.projectName, "project-name", "", "Project name to delete (used with --version).")
	f.StringVar(&opts.version, "version", "", "Project version to delete (used with --project-name).")
	f.BoolVar(&opts.yes, "yes", false, "Skip the confirmation prompt (non-interactive deletion).")
	cmd.MarkFlagsMutuallyExclusive("by-uuid", "project-name")
	cmd.MarkFlagsMutuallyExclusive("by-uuid", "version")
	cmd.MarkFlagsRequiredTogether("project-name", "version")

	return cmd
}

// runProjectDelete resolves the project identified by opts, shows a
// confirmation prompt (unless opts.yes), and deletes it.
func runProjectDelete(ctx context.Context, client *api.Client, opts *projectDeleteOptions, in io.Reader, out io.Writer) error {
	byUUID := strings.TrimSpace(opts.byUUID)
	name := strings.TrimSpace(opts.projectName)
	version := strings.TrimSpace(opts.version)

	if byUUID == "" && name == "" {
		return fmt.Errorf("the project must be identified with either --by-uuid or --project-name/--version")
	}

	var (
		target api.Project
		err    error
	)
	if byUUID != "" {
		target, err = client.GetProject(ctx, byUUID)
	} else {
		if version == "" {
			return fmt.Errorf("--version is required together with --project-name")
		}
		target, err = client.LookupProject(ctx, name, version)
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "About to delete project %s (uuid: %s). This cannot be undone.\n", target.Label(), target.UUID)

	if !opts.yes {
		reader := bufio.NewReader(in)
		fmt.Fprint(out, "Proceed? [y/N]: ")
		line, _ := reader.ReadString('\n')
		if !isYes(line) {
			fmt.Fprintln(out, "Aborted. Nothing was deleted.")
			return nil
		}
	}

	if err := client.DeleteProject(ctx, target.UUID); err != nil {
		return fmt.Errorf("deletion failed: %w", err)
	}
	fmt.Fprintf(out, "Deleted project %s (uuid: %s).\n", target.Label(), target.UUID)
	return nil
}
