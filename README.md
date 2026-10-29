# dtrack — Dependency-Track CLI (Go)

[![CI](https://github.com/eugenmayer/dtrack-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/eugenmayer/dtrack-cli/actions/workflows/ci.yml)
[![Release](https://github.com/eugenmayer/dtrack-cli/actions/workflows/release.yml/badge.svg)](https://github.com/eugenmayer/dtrack-cli/actions/workflows/release.yml)

A command-line client for [OWASP Dependency-Track](https://dependencytrack.org/) **5.x**.
Helps to upload boms, cloning projects for CI/CD automation and remove child projects or batch deactivating those.
It is and toolkit to make dtrack scale in CD/CD automations and administration, for multiple microservices of a project with
multiple versions (which can phase out via deacitivation)

Built on the official [`DependencyTrack/client-go`](https://github.com/DependencyTrack/client-go) library, adapted to the small, simplified project shape this CLI needs (see `internal/api`).

## Install

Download a prebuilt binary for your platform from the [latest release](https://github.com/eugenmayer/dtrack-cli/releases/latest),
extract it, and put `dtrack` on your `PATH`. Each release ships binaries for Linux, macOS, and Windows (amd64/arm64) plus a `checksums.txt`.

Or install from source with Go:

```bash
go install github.com/eugenmayer/dtrack-cli/cmd/dtrack@latest
```

or 

```bash
export DTRACK_VERSION=0.0.3
curl -sL "https://github.com/EugenMayer/dtrack-cli/releases/download/${DTRACK_VERSION}/dtrack_${DTRACK_VERSION}_linux_amd64.tar.gz" | tar xz
chmod +x ./dtrack
```

## Build

```bash
go build -o dtrack ./cmd/dtrack
```

Install into your `GOBIN`:

```bash
go install ./cmd/dtrack
```

## Configuration

The server URL lives in a YAML file in your home directory,
`~/.dtrack/config.yaml`; the API key is a secret and lives in your OS keyring
(GNOME Keyring, KWallet or KeePassXC via the Secret Service on Linux, the
Keychain on macOS, the Credential Manager on Windows).

```bash
dtrack auth login      # asks for the server URL (if not configured yet), then for the API key
dtrack auth status     # keyring backend, server URL, where the API key comes from (masked)
dtrack auth logout     # removes the API key from the keyring
```

`auth login` asks for the server URL only while none is configured (neither in
`~/.dtrack/config.yaml` nor via `DT_BASE_URL`), and saves it to the config
file. Anything else in that file, comments included, is kept. `--url` sets or
replaces the URL without asking. Then you paste the API key, which is hidden
as you type. It's created in Dependency-Track under *Administration → Access
Management → Teams → (team) → API Keys*. To script it, pipe the key in with
`--with-token` (pass `--url` too if no URL is configured yet, since stdin
then holds the key):

```bash
pass show work/dtrack | dtrack auth login --url https://dtrack.example.com --with-token
```

The key is stored under service `dtrack.api-key`, account `dtrack`, so other
tools can read it too (`secret-tool lookup service dtrack.api-key username dtrack`
on Linux, `security find-generic-password -s dtrack.api-key -a dtrack -w` on
macOS). Keyring handling comes from
[`ghcli/pkg/keychain`](https://github.com/EugenMayer/ghcli/tree/main/pkg/keychain).

The API key is taken from the first of these that has it:

1. the `DT_API_KEY` environment variable
2. a reference as the config file's `api-key`, for keys kept elsewhere:
   - `api-key: cmd:pass show work/dtrack` (output of a command: pass, gopass, 1Password, sops, …)
   - `api-key: env:MY_DT_KEY` (another environment variable)
   - `api-key: keychain:prod.api-key` (a different keyring path, e.g. one per server)
3. the OS keyring, path `api-key`

A plain-text API key in the config file (`api-key: odt_...`, including
`literal:odt_...`) is **not supported**: dtrack refuses to run while one is
there, even if `DT_API_KEY` or the keyring would supply the key. To migrate,
delete the `api-key` line from `~/.dtrack/config.yaml` and run
`dtrack auth login` to paste the key into the keyring.

No keyring available (SSH sessions, servers, WSL, CI)? dtrack notices that
right away instead of hanging, and falls through to the other sources. In
CI/CD or containers, use environment variables, which take precedence over
the file and need neither it nor a keyring:

```bash
export DT_BASE_URL=https://dtrack.example.com
export DT_API_KEY=odt_xxxxxxxx_...
```

If neither the file nor the environment variables supply a setting, the CLI
prints the path it looked for along with what to put there and how to store
the API key (or the environment variable names as an alternative). The API key needs project
**view** permissions for the
get and search commands, project **view** and **delete** permissions for the
cleanup and delete commands, project **view** and **edit** permissions for the
deactivate command, project **view** and **create** permissions for the
clone command (cloning creates a new project version), the permissions listed
under [`project children rescan`](#project-children-rescan) for that command, and **BOM Upload**
permission for the bom upload command (plus **Portfolio Management** or
**Project Creation & Upload** if using `--auto-create`).

TLS verification is the only connection setting kept on the command line: pass
`--insecure` to disable certificate verification (not recommended).

## Exit codes

Every command exits `0` on success and `1` on any error — including an
invalid or unauthorized API key, since Dependency-Track's `401`/`403`
responses are surfaced like any other request failure. Check the printed
`Error: ...` message (on stderr) for the reason.

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
`~/.dtrack/config.yaml` and the OS keyring):

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
`~/.dtrack/config.yaml` and the OS keyring, see [Configuration](#configuration)).

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

### `project children rescan`

Re-runs vulnerability analysis on the *current* BOM of every child of a
collection project — no re-upload needed. Handy after a vulnerability
database update or policy change, when you want fresh findings across all
services of a product right away.

```bash
dtrack project children rescan
```

You'll be asked which collection project to work on; unlike `cleanup` and
`deactivate`, there is no version prompt — every child is selected. The
overview is shown for confirmation, then an analysis is triggered for each
child and the command waits until all of them have completed.

```bash
dtrack project children rescan --collection "Product A@prod" --yes
```

Only rescan children of one version, and don't wait for completion:

```bash
dtrack project children rescan --collection "Product A" --version 1.2.3 --yes --no-wait
```

Flags:

| Flag | Description |
| --- | --- |
| `--collection NAME[@VERSION]` | Skip the picker; select a collection by name (append `@version` to disambiguate). |
| `--version REV` | Only rescan children with this version (default: all children). |
| `--include-inactive` | Whether inactive children are considered (default `false`, unlike `cleanup`/`deactivate`). |
| `--dry-run` | Show what would be rescanned, trigger nothing. |
| `--yes` | Skip the confirmation prompt. |
| `--no-wait` | Trigger the analyses and return immediately instead of waiting for them to finish. |

If an analysis of a child is already running, Dependency-Track refuses to
start another one (`409 Conflict`). dtrack reports that child as already in
progress, doesn't wait for it, and carries on with the rest.

Required permissions (Dependency-Track 5.x):

| Permission | Needed for |
| --- | --- |
| `VIEW_PORTFOLIO` | Listing collection projects and their children. |
| `VULNERABILITY_ANALYSIS` | Triggering the analysis (`POST /v1/finding/project/{uuid}/analyze`). |

With portfolio access control enabled, the API key's team also needs access
to every child being rescanned. Otherwise that child fails with `403` and the
rescan stops there. Polling for completion (`GET /v1/event/token/{uuid}`) needs
no extra permission. Dependency-Track 4.x protected the analyze endpoint with
`VIEW_VULNERABILITY` instead.

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml` and the OS keyring, see [Configuration](#configuration)).

### `project get`

Fetches a single project by UUID and prints its details.

```bash
dtrack project get d4e1f9d0-1234-5678-9abc-def012345678
```

```
Name:       Product A
Version:    1.10.0
UUID:       d4e1f9d0-1234-5678-9abc-def012345678
Classifier: APPLICATION
Active:     true
```

Flags:

| Flag | Description |
| --- | --- |
| `--json` | Print the project as JSON. |
| `--output-uuid` | Print only the project's uuid, and nothing else (mostly useful as a "does this uuid exist" check). Mutually exclusive with `--json`. |

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml` and the OS keyring, see [Configuration](#configuration)).

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
`~/.dtrack/config.yaml` and the OS keyring, see [Configuration](#configuration)).

### `project clone`

Clones a project into a new version. `NEW-VERSION` is the version assigned
to the clone; the source project is identified either directly by
`--by-uuid`, or by `--source-project-name` together with
`--source-project-version` (mutually exclusive with `--by-uuid`):

```bash
dtrack project clone 1.10.0 --source-project-name "Product A" --source-project-version prod
dtrack project clone 1.10.0 --by-uuid d4e1f9d0-1234-5678-9abc-def012345678
```

By default the clone carries **none** of the source project's tags,
properties, dependencies, components, services, audit history, ACL, or
policy violations over — opt in per-category with the `--include-*` flags:

```bash
dtrack project clone 1.10.0 \
  --source-project-name "Product A" --source-project-version prod \
  --include-components --include-dependencies --include-properties \
  --include-tags --include-acl --make-clone-latest
```

Cloning is processed **asynchronously** by the Dependency-Track server: this
command reports the tracking token immediately, then polls until processing
finishes and reports the resulting **cloned project** (its uuid, name, and
version) — not just the token:

```
Cloning Product A prod -> 1.10.0 (source uuid: d4e1f9d0-...)
Clone initiated. Token: 3f9a7b21-...
Clone is still being processed...
Clone completed: Product A 1.10.0 (uuid: 8b6a2f11-...)
```

Pass `--no-wait` to skip polling and return right after the clone starts,
reporting only the token (there is no project to resolve yet).

Flags:

| Flag | Description |
| --- | --- |
| `--by-uuid UUID` | Identify the source project directly by UUID, instead of `--source-project-name`/`--source-project-version`. |
| `--source-project-name NAME` | Source project name to clone (used with `--source-project-version`). |
| `--source-project-version REV` | Source project version to clone (used with `--source-project-name`). |
| `--include-tags` | Include tags in the clone. |
| `--include-properties` | Include properties in the clone. |
| `--include-dependencies` | Include dependencies (BOM) in the clone. |
| `--include-components` | Include components in the clone. |
| `--include-services` | Include services in the clone. |
| `--include-audit-history` | Include audit history (findings analysis/suppressions) in the clone. |
| `--include-acl` | Include the access control list in the clone. |
| `--include-policy-violations` | Include policy violations in the clone. |
| `--make-clone-latest` | Mark the cloned project as the latest version. |
| `--no-wait` | Report the tracking token and return immediately, without waiting for the clone to finish. |
| `--json` | Print the result as JSON: token, source project, and (unless `--no-wait`) the resolved cloned project. |
| `--output-uuid` | Print only the cloned project's uuid (or, with `--no-wait`, the tracking token), and nothing else. Mutually exclusive with `--json`. |

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml` and the OS keyring, see [Configuration](#configuration)).

### `project delete`

Deletes a single project (and everything under it — components, findings,
children — there is no undo). The project is identified either directly by
`--by-uuid`, or by `--project-name` together with `--version`:

```bash
dtrack project delete --by-uuid d4e1f9d0-1234-5678-9abc-def012345678
dtrack project delete --project-name "Product A" --version 1.9.0
```

Shows what it's about to delete and asks for confirmation unless `--yes` is
given:

```bash
dtrack project delete --project-name "Product A" --version 1.9.0 --yes
```

Flags:

| Flag | Description |
| --- | --- |
| `--by-uuid UUID` | Identify the project directly by UUID, instead of `--project-name`/`--version`. |
| `--project-name NAME` | Project name to delete (used with `--version`). |
| `--version REV` | Project version to delete (used with `--project-name`). |
| `--yes` | Skip the confirmation prompt (non-interactive deletion). |

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml` and the OS keyring, see [Configuration](#configuration)).

### `project bom upload`

Uploads a CycloneDX BOM (JSON or XML) to a project. The target project is
identified either by name or, directly, by UUID:

```bash
dtrack project bom upload bom.json --name "Product A" --version 1.10.0
dtrack project bom upload bom.json --by-uuid d4e1f9d0-1234-5678-9abc-def012345678
```

`--by-uuid` and `--name`/`--version` are mutually exclusive. With `--name`
and no matching project, pass `--auto-create` to have Dependency-Track
create it, optionally under a parent:

```bash
dtrack project bom upload bom.json \
  --name "New Service" --version 1.0.0 --auto-create \
  --parent-name "Product A" --parent-version prod \
  --is-latest
```

Uploads are processed **asynchronously**: the command prints the tracking
token immediately, then polls every couple of seconds and reports when
processing finishes (or times out after five minutes). Pass `--no-wait` to
skip polling and return right after the upload is accepted.

Pass `--skip-if-inactive` to look the project up first and skip the upload
— with a warning, but exit code `0` — if it already exists and is inactive.
This is meant for scripted/batch uploads where an inactive project is a
normal "nothing to do here" case rather than a failure:

```bash
dtrack project bom upload bom.json --name "Product A" --version 1.9.0 --skip-if-inactive
```

A project that doesn't exist yet is never treated as inactive: with
`--auto-create`, a not-found lookup just means the upload proceeds and
creates it (new projects are always active); without `--auto-create`, a
not-found project is still an error, same as without `--skip-if-inactive`.

Flags:

| Flag | Description |
| --- | --- |
| `--by-uuid UUID` | Identify the project directly by UUID, instead of `--name`/`--version`. |
| `--name NAME` | Project name to upload to (or auto-create). |
| `--version REV` | Project version to upload to (used with `--name`). |
| `--auto-create` | Create the project named by `--name`/`--version` if it doesn't already exist. |
| `--parent-name NAME` | Parent project name, used when auto-creating. |
| `--parent-version REV` | Parent project version, used when auto-creating. |
| `--parent-uuid UUID` | Parent project UUID, used when auto-creating. |
| `--is-latest` | Mark the uploaded BOM as belonging to the latest version of the project. |
| `--skip-if-inactive` | If the project already exists and is inactive, skip the upload with a warning (exit code `0`) instead of uploading to it. |
| `--no-wait` | Report the tracking token and return immediately, without waiting for processing to finish. |

Global flags: `--insecure` (connection URL and API key come from
`~/.dtrack/config.yaml` and the OS keyring, see [Configuration](#configuration)).

## Layout

```
cmd/dtrack/          main entry point
internal/api/        adapts DependencyTrack/client-go to this CLI's Project shape (v5 contract)
internal/config/     loads ~/.dtrack/config.yaml (url) and resolves the api-key (env, keyring, …)
internal/commands/   Cobra command tree + tests
```

## Notes on v5

This client targets the v5 REST API contract. Actual HTTP calls go through
[`DependencyTrack/client-go`](https://github.com/DependencyTrack/client-go)
(`internal/api` adapts it, rather than issuing requests directly), which
shapes a few behaviors:

- `api.New` (and so every command) makes one extra, unauthenticated
  `GET /api/version` call up front — client-go's constructor does this
  eagerly to learn the server version and validate connectivity, so
  constructing a client can now fail on a network/server error, not only a
  malformed URL or missing config.
- Children are fetched via `GET /v1/project/{uuid}/children` (the inline
  `children` array was removed in v5).
- Collection parents are identified by a non-empty `collectionLogic`.
- Bulk deletion has no client-go wrapper, so `BatchDelete` issues one
  `DELETE /v1/project/{uuid}` per project instead of a single
  `POST /v1/project/batchDelete` call.
- Deactivation uses a per-project partial update, `PATCH /v1/project/{uuid}`
  with `{"active": false}` — there is no bulk endpoint for toggling `active`.
  Dependency-Track responds `304 Not Modified` (not `200`) when the project
  is already inactive; the client treats that as success rather than an
  error, so deactivating an already-inactive child is a harmless no-op.
- Rescanning uses `POST /v1/finding/project/{uuid}/analyze`, one call per
  child (there is no bulk endpoint). Each returns a tracking token that is
  polled via `GET /v1/event/token/{uuid}`, like BOM uploads and clones.
- `project get` is a thin wrapper over `GET /v1/project/{uuid}`.
- `project search` filters `GET /v1/project` by `name`, which is an *exact*
  match server-side, not a substring/fuzzy search. Unlike the other list
  endpoints, this one call isn't paginated on the client side, so a project
  with more matching versions than a single page (100) would be truncated —
  an accepted, unlikely-in-practice limitation.
- `project delete` resolves `--project-name`/`--version` via
  `GET /v1/project/lookup` (an exact-match, single-result lookup) and
  `--by-uuid` via `GET /v1/project/{uuid}`, before deleting with
  `DELETE /v1/project/{uuid}`.
- `project clone` resolves the source project the same way `project delete`
  does (`GET /v1/project/lookup` for `--source-project-name`/
  `--source-project-version`, `GET /v1/project/{uuid}` for `--by-uuid`),
  then clones it via `PUT /v1/project/clone`, which only *starts* the clone
  and returns a tracking token — there is no bulk or synchronous variant.
  `--include-dependencies` has no distinct field on the wire: server-side, it
  has only ever done one thing — force `includeComponents` on ("for backward
  compatibility", per Dependency-Track's own source) — so it's sent as
  `includeComponents = includeComponents || includeDependencies`, which is
  exactly equivalent.
- `project bom upload` uses `PUT /v1/bom`, which likewise only *starts* the
  upload and returns a tracking token.
- Processing status for both clone and BOM upload tokens is polled via the
  same generic `GET /v1/event/token/{uuid}` (the older, upload-specific
  `/v1/bom/token/{uuid}` is deprecated in Dependency-Track in favor of it).
  Once a clone's token reports done, the resulting project is resolved via
  `GET /v1/project/lookup` using the source project's name and the new
  version — Dependency-Track's clone response itself carries only the
  token, never the cloned project's uuid.
- `project bom upload --skip-if-inactive` resolves the target project the
  same way `project delete` does (`GET /v1/project/{uuid}` for `--by-uuid`,
  `GET /v1/project/lookup` for `--name`/`--version`) before uploading, purely
  client-side — Dependency-Track's BOM upload endpoint has no server-side
  "skip if inactive" option.
- List endpoints are paginated (100/page); the client follows `X-Total-Count`.

## Testing

```bash
go test ./...
```

The command tests spin up an in-process mock Dependency-Track server and
exercise the full get, cleanup, deactivate, search, clone, delete, and bom
upload flows: interactive selection, dry-run, inactive filtering, the
no-match path, confirmation/abort, `--json`/`--output-uuid` output, both
`--by-uuid` and name/version project identification (across get, clone,
delete, and bom upload), BOM upload auto-create, processing polling for
both clone and BOM upload (including `--no-wait` and the poll timeout), and
`--skip-if-inactive` (inactive/active/not-found, with and without
`--auto-create`). The API client's tests separately confirm every request
method surfaces HTTP 401/403 as an error, which is what guarantees exit
code 1 on an auth failure for every command.
