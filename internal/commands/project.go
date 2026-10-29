package commands

import (
	"bufio"
	"context"
	"errors"
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
	childrenCmd.AddCommand(newChildrenDeactivateCmd(flags))
	childrenCmd.AddCommand(newChildrenRescanCmd(flags))
	projectCmd.AddCommand(childrenCmd)
	projectCmd.AddCommand(newProjectGetCmd(flags))
	projectCmd.AddCommand(newProjectSearchCmd(flags))
	projectCmd.AddCommand(newProjectCloneCmd(flags))
	projectCmd.AddCommand(newProjectDeleteCmd(flags))
	projectCmd.AddCommand(newBomCmd(flags))
	return projectCmd
}

// childrenActionOptions holds the flags shared by the "children" subcommands
// that walk a collection's children and apply a bulk operation to those
// matching a given version (cleanup, deactivate, rescan).
type childrenActionOptions struct {
	collection      string
	revision        string
	includeInactive bool
	yes             bool
	dryRun          bool
	noWait          bool // rescan only
}

func newChildrenCleanupCmd(flags *rootFlags) *cobra.Command {
	opts := &childrenActionOptions{includeInactive: true}

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete all children of a collection project matching a given version",
		Long: `Delete all children of a collection project matching a given version.

Walks every direct child of the chosen collection project, aggregates those
whose version matches the one you specify, shows an overview for confirmation,
and deletes them once confirmed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := flags.newClient(cmd.Context())
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

func newChildrenDeactivateCmd(flags *rootFlags) *cobra.Command {
	opts := &childrenActionOptions{includeInactive: true}

	cmd := &cobra.Command{
		Use:   "deactivate",
		Short: "Deactivate all children of a collection project matching a given version",
		Long: `Deactivate all children of a collection project matching a given version.

Works just like "project children cleanup", except matched children are
deactivated instead of deleted: walks every direct child of the chosen
collection project, aggregates those whose version matches the one you
specify, shows an overview for confirmation, and deactivates them once
confirmed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := flags.newClient(cmd.Context())
			if err != nil {
				return err
			}
			return runDeactivate(cmd.Context(), client, opts, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.collection, "collection", "",
		"Collection project name to work on (skips the interactive picker). "+
			"Append '@<version>' to disambiguate collections that share a name.")
	f.StringVar(&opts.revision, "version", "",
		"Child project version/revision to deactivate (skips the prompt).")
	f.BoolVar(&opts.includeInactive, "include-inactive", true,
		"Whether inactive child projects are considered.")
	f.BoolVar(&opts.yes, "yes", false, "Skip the confirmation prompt (non-interactive deactivation).")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Show what would be deactivated without deactivating anything.")

	return cmd
}

func newChildrenRescanCmd(flags *rootFlags) *cobra.Command {
	opts := &childrenActionOptions{}

	cmd := &cobra.Command{
		Use:   "rescan",
		Short: "Re-analyze the current BOM of all children of a collection project",
		Long: `Re-analyze the current BOM of all children of a collection project.

Walks every direct child of the chosen collection project, shows an overview
for confirmation, and triggers a vulnerability re-analysis of each child's
current BOM (no re-upload needed) once confirmed. Pass --version to only
rescan children of that version.

Inactive children are skipped unless --include-inactive is given.

Dependency-Track runs each analysis asynchronously: unless --no-wait is
given, this command polls until every triggered analysis has completed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := flags.newClient(cmd.Context())
			if err != nil {
				return err
			}
			return runRescan(cmd.Context(), client, opts, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.collection, "collection", "",
		"Collection project name to work on (skips the interactive picker). "+
			"Append '@<version>' to disambiguate collections that share a name.")
	f.StringVar(&opts.revision, "version", "",
		"Only rescan children with this version/revision (default: all children).")
	f.BoolVar(&opts.includeInactive, "include-inactive", false,
		"Whether inactive child projects are considered.")
	f.BoolVar(&opts.yes, "yes", false, "Skip the confirmation prompt (non-interactive rescan).")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Show what would be rescanned without triggering anything.")
	f.BoolVar(&opts.noWait, "no-wait", false,
		"Trigger the analyses and return immediately, without waiting for them to finish.")

	return cmd
}

// childrenAction describes the verb-specific parts of a "children" command
// that walks matching children and applies a bulk operation to them.
type childrenAction struct {
	verb        string // present tense, used in prompts: "delete", "deactivate"
	noun        string // used in the wrapped error: "deletion", "deactivation"
	pastTense   string // lowercase past tense: "deleted", "deactivated"
	confirmNote string // appended to the confirmation prompt, e.g. " This cannot be undone"
	// versionOptional skips the version prompt; an empty version then
	// matches every child instead of being an error.
	versionOptional bool
	apply           func(ctx context.Context, client *api.Client, projects []api.Project, out io.Writer) error
	// summary, if set, overrides the default "<PastTense> N project(s)."
	// line printed once apply succeeds.
	summary func(n int) string
}

// projectUUIDs returns the uuids of projects, in order.
func projectUUIDs(projects []api.Project) []string {
	uuids := make([]string, len(projects))
	for i, p := range projects {
		uuids[i] = p.UUID
	}
	return uuids
}

func deleteAction() childrenAction {
	return childrenAction{
		verb:        "delete",
		noun:        "deletion",
		pastTense:   "deleted",
		confirmNote: " This cannot be undone",
		apply: func(ctx context.Context, client *api.Client, projects []api.Project, _ io.Writer) error {
			return client.BatchDelete(ctx, projectUUIDs(projects))
		},
	}
}

func deactivateAction() childrenAction {
	return childrenAction{
		verb:      "deactivate",
		noun:      "deactivation",
		pastTense: "deactivated",
		apply: func(ctx context.Context, client *api.Client, projects []api.Project, _ io.Writer) error {
			return client.BatchDeactivate(ctx, projectUUIDs(projects))
		},
	}
}

// rescanAction triggers a re-analysis of each project's current BOM and,
// unless noWait, waits for every analysis to finish. All analyses are
// triggered up front so the server can work on them concurrently.
func rescanAction(noWait bool) childrenAction {
	return childrenAction{
		verb:            "rescan",
		noun:            "rescan",
		pastTense:       "rescanned",
		versionOptional: true,
		apply: func(ctx context.Context, client *api.Client, projects []api.Project, out io.Writer) error {
			// An empty token marks a project whose analysis was already
			// running: the server refuses a second one, and there is nothing
			// of ours to wait for.
			tokens := make([]string, len(projects))
			for i, p := range projects {
				token, err := client.AnalyzeProject(ctx, p.UUID)
				if errors.Is(err, api.ErrAnalysisInProgress) {
					fmt.Fprintf(out, "Analysis of %s is already in progress; not triggering another one.\n", p.Label())
					continue
				}
				if err != nil {
					return fmt.Errorf("triggering analysis of %s (uuid: %s): %w", p.Label(), p.UUID, err)
				}
				tokens[i] = token
				fmt.Fprintf(out, "Triggered analysis of %s. Token: %s\n", p.Label(), token)
			}
			if noWait {
				return nil
			}
			for i, p := range projects {
				if tokens[i] == "" {
					continue
				}
				msg := fmt.Sprintf("Analysis of %s is still running...", p.Label())
				if err := waitForTokenProcessing(ctx, client, tokens[i], msg, out); err != nil {
					return err
				}
				fmt.Fprintf(out, "Analysis of %s completed.\n", p.Label())
			}
			return nil
		},
		summary: func(n int) string {
			if noWait {
				return fmt.Sprintf("Triggered analysis of %d project(s); not waiting for completion.", n)
			}
			return fmt.Sprintf("Rescanned %d project(s).", n)
		},
	}
}

// runCleanup implements the cleanup flow. Input and output are injected so the
// command is testable.
func runCleanup(ctx context.Context, client *api.Client, opts *childrenActionOptions, in io.Reader, out io.Writer) error {
	return runChildrenAction(ctx, client, opts, in, out, deleteAction())
}

// runDeactivate implements the deactivate flow: identical to runCleanup
// except matched children are deactivated rather than deleted.
func runDeactivate(ctx context.Context, client *api.Client, opts *childrenActionOptions, in io.Reader, out io.Writer) error {
	return runChildrenAction(ctx, client, opts, in, out, deactivateAction())
}

// runRescan implements the rescan flow: like runCleanup, except the version
// is optional (all children by default) and matched children have their
// current BOM re-analyzed rather than being deleted.
func runRescan(ctx context.Context, client *api.Client, opts *childrenActionOptions, in io.Reader, out io.Writer) error {
	return runChildrenAction(ctx, client, opts, in, out, rescanAction(opts.noWait))
}

// runChildrenAction implements the shared walk/confirm/apply flow for the
// "children" subcommands: pick a collection project, ask for the child
// version to act on, aggregate the matching children, show an overview for
// confirmation, and apply the action once confirmed.
func runChildrenAction(ctx context.Context, client *api.Client, opts *childrenActionOptions, in io.Reader, out io.Writer, action childrenAction) error {
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
		parent, err = matchNamedProject(collections, opts.collection)
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

	// 2. Ask for the revision to act on, unless supplied or optional.
	revision := strings.TrimSpace(opts.revision)
	if revision == "" && !action.versionOptional {
		fmt.Fprintf(out, "Enter the child project version to %s: ", action.verb)
		line, rerr := reader.ReadString('\n')
		if rerr != nil && line == "" {
			return fmt.Errorf("reading version: %w", rerr)
		}
		revision = strings.TrimSpace(line)
	}
	if revision == "" && !action.versionOptional {
		return fmt.Errorf("version must not be empty")
	}

	// 3. Gather children and aggregate the matches.
	allChildren, err := client.ListChildren(ctx, parent.UUID, excludeInactive)
	if err != nil {
		return err
	}
	var matches []api.Project
	for _, c := range allChildren {
		if revision == "" || c.Version == revision {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		if revision == "" {
			fmt.Fprintf(out, "'%s' has no children. Nothing to do.\n", parent.Label())
		} else {
			fmt.Fprintf(out, "No children of '%s' match version '%s'. Nothing to do.\n", parent.Label(), revision)
		}
		return nil
	}

	// 4. Overview / confirmation step.
	renderOverview(matches, revision, parent, out)

	if opts.dryRun {
		fmt.Fprintf(out, "Dry run: no projects were %s.\n", action.pastTense)
		return nil
	}

	if !opts.yes {
		fmt.Fprintf(out, "%s these %d project(s)?%s [y/N]: ", capitalize(action.verb), len(matches), action.confirmNote)
		line, _ := reader.ReadString('\n')
		if !isYes(line) {
			fmt.Fprintf(out, "Aborted. Nothing was %s.\n", action.pastTense)
			return nil
		}
	}

	// 5. Apply.
	if err := action.apply(ctx, client, matches, out); err != nil {
		return fmt.Errorf("%s failed: %w", action.noun, err)
	}
	if action.summary != nil {
		fmt.Fprintln(out, action.summary(len(matches)))
	} else {
		fmt.Fprintf(out, "%s %d project(s).\n", capitalize(action.pastTense), len(matches))
	}
	return nil
}

// capitalize upper-cases the first byte of s. Only used for the short,
// all-ASCII verbs in childrenAction, so a byte-wise upper-case is sufficient.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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

// renderOverview lists matches; an empty version means "all children".
func renderOverview(matches []api.Project, version string, parent api.Project, out io.Writer) {
	if version == "" {
		fmt.Fprintf(out, "The following %d child project(s) of '%s' are selected:\n\n",
			len(matches), parent.Label())
	} else {
		fmt.Fprintf(out, "The following %d child project(s) of '%s' match version '%s':\n\n",
			len(matches), parent.Label(), version)
	}

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

// matchNamedProject resolves a "name" or "name@version" spec to exactly one
// project out of candidates.
func matchNamedProject(candidates []api.Project, spec string) (api.Project, error) {
	name := spec
	version := ""
	if i := strings.Index(spec, "@"); i >= 0 {
		name = spec[:i]
		version = spec[i+1:]
	}

	var matches []api.Project
	for _, c := range candidates {
		if c.Name != name {
			continue
		}
		if version != "" && c.Version != version {
			continue
		}
		matches = append(matches, c)
	}

	switch len(matches) {
	case 0:
		return api.Project{}, fmt.Errorf("no project matches %q", spec)
	case 1:
		return matches[0], nil
	default:
		labels := make([]string, len(matches))
		for i, c := range matches {
			if c.Version != "" {
				labels[i] = c.Name + "@" + c.Version
			} else {
				labels[i] = c.Name
			}
		}
		return api.Project{}, fmt.Errorf(
			"%q is ambiguous (%d matches: %s); disambiguate with '<name>@<version>'",
			spec, len(matches), strings.Join(labels, ", "))
	}
}

func isYes(line string) bool {
	s := strings.ToLower(strings.TrimSpace(line))
	return s == "y" || s == "yes"
}
