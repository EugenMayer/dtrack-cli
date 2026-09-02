package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/spf13/cobra"
)

// newProjectCmd builds the "project" group and its subgroups/commands.
func newProjectCmd(flags *rootFlags) *cobra.Command {
	projectCmd := &cobra.Command{
		Use:   "project",
		Short: "Commands for working with projects",
	}

	childrenCmd := &cobra.Command{
		Use:   "children",
		Short: "Commands that operate on the children of a collection project",
	}

	childrenCmd.AddCommand(newChildrenCleanupCmd(flags))
	projectCmd.AddCommand(childrenCmd)
	return projectCmd
}

// cleanupOptions holds the flags specific to "project children cleanup".
type cleanupOptions struct {
	collection      string
	revision        string
	includeInactive bool
	yes             bool
	dryRun          bool
}

func newChildrenCleanupCmd(flags *rootFlags) *cobra.Command {
	opts := &cleanupOptions{includeInactive: true}

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete all children of a collection project matching a given version",
		Long: `Delete all children of a collection project matching a given version.

Walks every direct child of the chosen collection project, aggregates those
whose version matches the one you specify, shows an overview for confirmation,
and deletes them once confirmed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := flags.newClient()
			if err != nil {
				return err
			}
			return runCleanup(cmd.Context(), client, opts, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.collection, "collection", "",
		"Collection project name to work on (skips the interactive picker). "+
			"Append '@<version>' to disambiguate collections that share a name.")
	f.StringVar(&opts.revision, "version", "",
		"Child project version/revision to delete (skips the prompt).")
	f.BoolVar(&opts.includeInactive, "include-inactive", true,
		"Whether inactive child projects are considered.")
	f.BoolVar(&opts.yes, "yes", false, "Skip the confirmation prompt (non-interactive deletion).")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Show what would be deleted without deleting anything.")

	return cmd
}

// runCleanup implements the cleanup flow. Input and output are injected so the
// command is testable.
func runCleanup(ctx context.Context, client *api.Client, opts *cleanupOptions, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	excludeInactive := !opts.includeInactive

	// 1. Choose the collection project (interactively unless named).
	collections, err := client.ListCollectionProjects(ctx, excludeInactive)
	if err != nil {
		return err
	}
	if len(collections) == 0 {
		return fmt.Errorf("no collection projects found on the server")
	}

	var parent api.Project
	if opts.collection != "" {
		parent, err = matchNamedCollection(collections, opts.collection)
		if err != nil {
			return err
		}
	} else {
		parent, err = pickCollectionProject(collections, reader, out)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "\nWorking on collection project: %s\n\n", parent.Label())

	// 2. Ask for the revision to delete, unless supplied.
	revision := strings.TrimSpace(opts.revision)
	if revision == "" {
		fmt.Fprint(out, "Enter the child project version to delete: ")
		line, rerr := reader.ReadString('\n')
		if rerr != nil && line == "" {
			return fmt.Errorf("reading version: %w", rerr)
		}
		revision = strings.TrimSpace(line)
	}
	if revision == "" {
		return fmt.Errorf("version must not be empty")
	}

	// 3. Gather children and aggregate the matches.
	allChildren, err := client.ListChildren(ctx, parent.UUID, excludeInactive)
	if err != nil {
		return err
	}
	var matches []api.Project
	for _, c := range allChildren {
		if c.Version == revision {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(out, "No children of '%s' match version '%s'. Nothing to do.\n", parent.Label(), revision)
		return nil
	}

	// 4. Overview / confirmation step.
	renderOverview(matches, revision, parent, out)

	if opts.dryRun {
		fmt.Fprintln(out, "Dry run: no projects were deleted.")
		return nil
	}

	if !opts.yes {
		fmt.Fprintf(out, "Delete these %d project(s)? This cannot be undone [y/N]: ", len(matches))
		line, _ := reader.ReadString('\n')
		if !isYes(line) {
			fmt.Fprintln(out, "Aborted. Nothing was deleted.")
			return nil
		}
	}

	// 5. Delete.
	uuids := make([]string, len(matches))
	for i, m := range matches {
		uuids[i] = m.UUID
	}
	if err := client.BatchDelete(ctx, uuids); err != nil {
		return fmt.Errorf("deletion failed: %w", err)
	}
	fmt.Fprintf(out, "Deleted %d project(s).\n", len(matches))
	return nil
}

func pickCollectionProject(collections []api.Project, reader *bufio.Reader, out io.Writer) (api.Project, error) {
	fmt.Fprintln(out, "Collection projects:")
	for i, p := range collections {
		version := ""
		if p.Version != "" {
			version = fmt.Sprintf("  (version: %s)", p.Version)
		}
		state := ""
		if !p.Active {
			state = "  [inactive]"
		}
		fmt.Fprintf(out, "  %3d. %s%s   [%s]%s\n", i+1, p.Name, version, p.CollectionLogic, state)
	}
	fmt.Fprintf(out, "Select a collection project by number: ")

	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return api.Project{}, fmt.Errorf("reading selection: %w", err)
	}
	choice, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || choice < 1 || choice > len(collections) {
		return api.Project{}, fmt.Errorf("invalid selection: %q", strings.TrimSpace(line))
	}
	return collections[choice-1], nil
}

func renderOverview(matches []api.Project, version string, parent api.Project, out io.Writer) {
	fmt.Fprintf(out, "The following %d child project(s) of '%s' match version '%s':\n\n",
		len(matches), parent.Label(), version)

	nameWidth := 4
	for _, m := range matches {
		if len(m.Name) > nameWidth {
			nameWidth = len(m.Name)
		}
	}
	for _, m := range matches {
		state := ""
		if !m.Active {
			state = "  [inactive]"
		}
		fmt.Fprintf(out, "  - %-*s   %s   %s%s\n", nameWidth, m.Name, m.Version, m.UUID, state)
	}
	fmt.Fprintln(out)
}

// matchNamedCollection resolves a --collection value to exactly one collection
// project. The value may be "name" or "name@version".
func matchNamedCollection(collections []api.Project, spec string) (api.Project, error) {
	name := spec
	version := ""
	if i := strings.Index(spec, "@"); i >= 0 {
		name = spec[:i]
		version = spec[i+1:]
	}

	var candidates []api.Project
	for _, c := range collections {
		if c.Name != name {
			continue
		}
		if version != "" && c.Version != version {
			continue
		}
		candidates = append(candidates, c)
	}

	switch len(candidates) {
	case 0:
		return api.Project{}, fmt.Errorf("no collection project matches %q", spec)
	case 1:
		return candidates[0], nil
	default:
		labels := make([]string, len(candidates))
		for i, c := range candidates {
			if c.Version != "" {
				labels[i] = c.Name + "@" + c.Version
			} else {
				labels[i] = c.Name
			}
		}
		return api.Project{}, fmt.Errorf(
			"%q is ambiguous (%d matches: %s); disambiguate with '<name>@<version>'",
			spec, len(candidates), strings.Join(labels, ", "))
	}
}

func isYes(line string) bool {
	s := strings.ToLower(strings.TrimSpace(line))
	return s == "y" || s == "yes"
}
