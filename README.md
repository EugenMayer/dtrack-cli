# dtrack — Dependency-Track CLI (Go)

[![CI](https://github.com/eugenmayer/dtrack-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/eugenmayer/dtrack-cli/actions/workflows/ci.yml)
[![Release](https://github.com/eugenmayer/dtrack-cli/actions/workflows/release.yml/badge.svg)](https://github.com/eugenmayer/dtrack-cli/actions/workflows/release.yml)

A command-line client for [OWASP Dependency-Track](https://dependencytrack.org/)
**5.x** (tested against 5.1), written in Go 1.27. Commands are organized as
Cobra groups and subgroups so more can be added over time.

## Requirements

- Go 1.27 or newer.

## Install

Download a prebuilt binary for your platform from the
[latest release](https://github.com/eugenmayer/dtrack-cli/releases/latest),
extract it, and put `dtrack` on your `PATH`. Each release ships binaries for
Linux, macOS, and Windows (amd64/arm64) plus a `checksums.txt`.

Or install from source with Go:

```bash
go install github.com/eugenmayer/dtrack-cli/cmd/dtrack@latest
```

## Build

```bash
go mod tidy   # completes go.sum from your module proxy (see note below)
go build -o dtrack ./cmd/dtrack
```

> **Note on `go.sum`:** this project was assembled in a sandbox without access
> to `proxy.golang.org` / `gopkg.in`, so the committed `go.sum` covers only the
> modules that were reachable there. Running `go mod tidy` once in a normal
> environment fills in the remaining transitive hashes (all standard,
> upstream-published modules — `cobra` and its deps). The build and full test
> suite pass on Go 1.27.0.

Install into your `GOBIN`:

```bash
go install ./cmd/dtrack
```

## Configuration

Connection settings are read from a YAML file in your home directory,
`~/.dtrack/config.yaml`:

```yaml
url: https://dtrack.example.com
api-key: odt_xxxxxxxx_...
```

Create it once:

```bash
mkdir -p ~/.dtrack
cat > ~/.dtrack/config.yaml <<'EOF'
url: https://dtrack.example.com
api-key: odt_xxxxxxxx_...
EOF
chmod 600 ~/.dtrack/config.yaml
```

If the file is missing, the CLI prints the path it looked for along with a
template to create. The API key needs project **view** permissions for the
search command, project **view** and **delete** permissions for the cleanup
command, project **view** and **edit** permissions for the deactivate
command, and project **view** and **create** permissions for the clone
command (cloning creates a new project version).

TLS verification is the only connection setting kept on the command line: pass
`--insecure` to disable certificate verification (not recommended).

## Commands

### `project children cleanup`

Deletes all children of a collection project that match a given version.

Interactive (the default):

```bash
dtrack project children cleanup
```

You'll be asked which collection project to work on, then for the child
version to delete. Matching children are aggregated and shown as an overview;
nothing is deleted until you confirm.

Non-interactive / scripted (connection details come from
`~/.dtrack/config.yaml`):

```bash
dtrack project children cleanup \
  --collection "Product A@prod" \
  --version 1.2.3 \
  --include-inactive=false \
  --yes
```

Preview only, without deleting:

```bash
dtrack project children cleanup --collection "Product A" --version 1.2.3 --dry-run
```

Flags:

| Flag | Description |
| --- | --- |
| `--collection NAME[@VERSION]` | Skip the picker; select a collection by name (append `@version` to disambiguate). |
| `--version REV` | Skip the prompt; the child version/revision to delete. |
| `--include-inactive` | Whether inactive children are considered (default `true`; pass `--include-inactive=false` to exclude). |
| `--dry-run` | Show what would be deleted, delete nothing. |
| `--yes` | Skip the confirmation prompt. |

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml`, see [Configuration](#configuration)).

### `project children deactivate`

Works exactly like `project children cleanup`, except matching children are
deactivated (their `active` flag is set to `false`) instead of deleted — a
reversible alternative to cleanup for when you'd rather retire old versions
than remove them outright.

```bash
dtrack project children deactivate
```

```bash
dtrack project children deactivate \
  --collection "Product A@prod" \
  --version 1.2.3 \
  --include-inactive=false \
  --yes
```

Preview only, without deactivating:

```bash
dtrack project children deactivate --collection "Product A" --version 1.2.3 --dry-run
```

It takes the same `--collection`, `--version`, `--include-inactive`,
`--dry-run`, and `--yes` flags as `cleanup` (see above), only applied to
deactivation instead of deletion.

### `project search`

Looks up every version of a project by its exact name and prints them.

```bash
dtrack project search "Product A"
```

```
2 project(s) found:

  Product A   prod   d4e1f9d0-...
  Product A   1.9.0  8b6a2f11-...  [inactive]
```

Narrow to one version, or exclude inactive projects:

```bash
dtrack project search "Product A" --version prod
dtrack project search "Product A" --only-active
```

Flags:

| Flag | Description |
| --- | --- |
| `--version REV` | Restrict results to this exact version. |
| `--only-active` | Only include active projects. |
| `--json` | Print results as JSON instead of the table above. |
| `--output-uuid` | Print only the matching project uuid(s), one per line, and nothing else — handy for piping into another command. Mutually exclusive with `--json`. |

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml`, see [Configuration](#configuration)).

### `project clone`

Clones a project into a new version. `NAME` identifies the source project
(append `@<version>` to disambiguate a name that has multiple versions);
`NEW-VERSION` is the version assigned to the clone.

```bash
dtrack project clone "Product A@prod" 1.10.0
```

By default the clone carries **none** of the source project's tags,
properties, dependencies, components, services, audit history, ACL, or
policy violations over — opt in per-category with the `--include-*` flags:

```bash
dtrack project clone "Product A@prod" 1.10.0 \
  --include-components --include-dependencies --include-properties \
  --include-tags --include-acl --make-clone-latest
```

Cloning is processed **asynchronously** by the Dependency-Track server: this
command reports the tracking token it returns immediately, not the finished
project. Use the Dependency-Track UI, or poll the server directly, to know
when the clone has finished.

Flags:

| Flag | Description |
| --- | --- |
| `--include-tags` | Include tags in the clone. |
| `--include-properties` | Include properties in the clone. |
| `--include-dependencies` | Include dependencies (BOM) in the clone. |
| `--include-components` | Include components in the clone. |
| `--include-services` | Include services in the clone. |
| `--include-audit-history` | Include audit history (findings analysis/suppressions) in the clone. |
| `--include-acl` | Include the access control list in the clone. |
| `--include-policy-violations` | Include policy violations in the clone. |
| `--make-clone-latest` | Mark the cloned project as the latest version. |
| `--json` | Print the result (token and resolved source project) as JSON. |
| `--output-uuid` | Print only the clone's tracking token, and nothing else. Mutually exclusive with `--json`. |

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml`, see [Configuration](#configuration)).

## Layout

```
cmd/dtrack/          main entry point
internal/api/        Dependency-Track REST client (v5 contract)
internal/config/     loads ~/.dtrack/config.yaml (url + api-key)
internal/commands/   Cobra command tree + tests
```

## Notes on v5

This client targets the v5 REST API contract:

- Children are fetched via `GET /v1/project/{uuid}/children` (the inline
  `children` array was removed in v5).
- Collection parents are identified by a non-empty `collectionLogic`.
- Bulk deletion uses `POST /v1/project/batchDelete`, with a per-project
  `DELETE` fallback.
- Deactivation uses a per-project partial update, `PATCH /v1/project/{uuid}`
  with `{"active": false}` — there is no bulk endpoint for toggling `active`.
- `project search` filters `GET /v1/project` by `name`, which is an *exact*
  match server-side, not a substring/fuzzy search.
- `project clone` uses `PUT /v1/project/clone`, which only *starts* the clone
  and returns a tracking token — there is no bulk or synchronous variant.
- List endpoints are paginated (100/page); the client follows `X-Total-Count`.

## Testing

```bash
go test ./...
```

The command tests spin up an in-process mock Dependency-Track server and
exercise the full cleanup, deactivate, search, and clone flows: interactive
selection, dry-run, inactive filtering, the no-match path,
confirmation/abort, source disambiguation, and `--json`/`--output-uuid`
output.
