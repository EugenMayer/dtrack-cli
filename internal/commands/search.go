package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/spf13/cobra"
)

// searchOptions holds the flags for "project search".
type searchOptions struct {
	name       string
	version    string
	onlyActive bool
	jsonOutput bool
	outputUUID bool
}

func newProjectSearchCmd(flags *rootFlags) *cobra.Command {
	opts := &searchOptions{}

	cmd := &cobra.Command{
		Use:   "search <name>",
		Short: "Search for projects by exact name",
		Long: `Search for projects by exact name.

Prints every version of the project found under NAME. Use --version to
narrow the results down to one exact version, and --only-active to exclude
inactive projects.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.name = args[0]
			client, err := flags.newClient()
			if err != nil {
				return err
			}
			return runSearch(cmd.Context(), client, opts, cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.version, "version", "", "Restrict results to this exact version.")
	f.BoolVar(&opts.onlyActive, "only-active", false, "Only include active projects.")
	f.BoolVar(&opts.jsonOutput, "json", false, "Print results as JSON.")
	f.BoolVar(&opts.outputUUID, "output-uuid", false,
		"Print only the matching project uuid(s), one per line, and nothing else.")
	cmd.MarkFlagsMutuallyExclusive("json", "output-uuid")

	return cmd
}

// runSearch implements the search flow: look up every version of a project by
// exact name, then narrow it down by version and active state as requested.
func runSearch(ctx context.Context, client *api.Client, opts *searchOptions, out io.Writer) error {
	projects, err := client.ListProjectsByName(ctx, opts.name, opts.onlyActive)
	if err != nil {
		return err
	}

	version := strings.TrimSpace(opts.version)
	matches := make([]api.Project, 0, len(projects))
	for _, p := range projects {
		if version != "" && p.Version != version {
			continue
		}
		matches = append(matches, p)
	}

	if opts.outputUUID {
		for _, p := range matches {
			fmt.Fprintln(out, p.UUID)
		}
		return nil
	}

	if opts.jsonOutput {
		return printJSON(out, matches)
	}

	if len(matches) == 0 {
		fmt.Fprintf(out, "No projects found matching name %q", opts.name)
		if version != "" {
			fmt.Fprintf(out, " and version %q", version)
		}
		fmt.Fprintln(out, ".")
		return nil
	}

	fmt.Fprintf(out, "%d project(s) found:\n\n", len(matches))
	renderProjectList(matches, out)
	return nil
}

// renderProjectList prints one line per project: name, version, uuid, and any
// state flags (inactive, collection).
func renderProjectList(projects []api.Project, out io.Writer) {
	nameWidth := 4
	versionWidth := 7
	for _, p := range projects {
		if len(p.Name) > nameWidth {
			nameWidth = len(p.Name)
		}
		if len(p.Version) > versionWidth {
			versionWidth = len(p.Version)
		}
	}
	for _, p := range projects {
		state := ""
		if !p.Active {
			state = "  [inactive]"
		}
		collection := ""
		if p.IsCollection() {
			collection = "  [collection]"
		}
		fmt.Fprintf(out, "  %-*s   %-*s   %s%s%s\n", nameWidth, p.Name, versionWidth, p.Version, p.UUID, state, collection)
	}
}
