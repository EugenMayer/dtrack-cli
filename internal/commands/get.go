package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/spf13/cobra"
)

// getOptions holds the flags for "project get".
type getOptions struct {
	jsonOutput bool
	outputUUID bool
}

func newProjectGetCmd(flags *rootFlags) *cobra.Command {
	opts := &getOptions{}

	cmd := &cobra.Command{
		Use:   "get <uuid>",
		Short: "Get a single project by UUID",
		Long:  `Get a single project by UUID and print its details.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := flags.newClient()
			if err != nil {
				return err
			}
			return runGet(cmd.Context(), client, args[0], opts, cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.BoolVar(&opts.jsonOutput, "json", false, "Print the project as JSON.")
	f.BoolVar(&opts.outputUUID, "output-uuid", false, "Print only the project's uuid, and nothing else.")
	cmd.MarkFlagsMutuallyExclusive("json", "output-uuid")

	return cmd
}

// runGet fetches the project identified by uuid and reports it.
func runGet(ctx context.Context, client *api.Client, uuid string, opts *getOptions, out io.Writer) error {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return fmt.Errorf("uuid must not be empty")
	}

	project, err := client.GetProject(ctx, uuid)
	if err != nil {
		return err
	}

	if opts.outputUUID {
		fmt.Fprintln(out, project.UUID)
		return nil
	}
	if opts.jsonOutput {
		return printJSON(out, project)
	}

	renderProjectDetail(project, out)
	return nil
}

// renderProjectDetail prints a project's fields, one per line.
func renderProjectDetail(p api.Project, out io.Writer) {
	fmt.Fprintf(out, "Name:       %s\n", p.Name)
	fmt.Fprintf(out, "Version:    %s\n", p.Version)
	fmt.Fprintf(out, "UUID:       %s\n", p.UUID)
	if p.Classifier != "" {
		fmt.Fprintf(out, "Classifier: %s\n", p.Classifier)
	}
	fmt.Fprintf(out, "Active:     %t\n", p.Active)
	if p.IsCollection() {
		fmt.Fprintf(out, "Collection: %s\n", p.CollectionLogic)
	}
}
