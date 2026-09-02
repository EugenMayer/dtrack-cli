# dtrack — Dependency-Track CLI (Go)

A command-line client for [OWASP Dependency-Track](https://dependencytrack.org/)
**5.x** (tested against 5.1), written in Go 1.27. Commands are organized as
Cobra groups and subgroups so more can be added over time.

## Requirements

- Go 1.27 or newer.

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
template to create. The API key needs project **view** and **delete**
permissions for the cleanup command.

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
- List endpoints are paginated (100/page); the client follows `X-Total-Count`.

## Testing

```bash
go test ./...
```

The command tests spin up an in-process mock Dependency-Track server and
exercise the full cleanup flow: interactive selection, dry-run, inactive
filtering, the no-match path, and confirmation/abort.
