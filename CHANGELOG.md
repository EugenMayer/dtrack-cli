# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `project children cleanup` command: interactively (or via flags) selects a
  collection project, aggregates its direct children matching a given version,
  shows an overview, and deletes them on confirmation.
- `project children deactivate` command: same flow as `cleanup`, but sets the
  matched children's `active` flag to `false` instead of deleting them.
- Flags for the cleanup and deactivate commands: `--collection`, `--version`,
  `--include-inactive`, `--dry-run`, `--yes`.
- `project get <uuid>` command: fetches a single project by UUID and prints
  its details, with `--json` and `--output-uuid` output flags (mutually
  exclusive).
- `project search <name>` command: looks up every version of a project by
  exact name, with `--version` to narrow to one version and `--only-active`
  to exclude inactive projects.
- `project clone <new-version>` command: clones a project into a new
  version. The source project is identified either by `--by-uuid` or by
  `--source-project-name`/`--source-project-version` (mutually exclusive),
  with `--include-tags`, `--include-properties`, `--include-dependencies`,
  `--include-components`, `--include-services`, `--include-audit-history`,
  `--include-acl`, `--include-policy-violations`, and `--make-clone-latest`
  flags mirroring Dependency-Track's clone options. Cloning is asynchronous
  server-side: the command reports the tracking token, then polls (via the
  same `GET /v1/event/token/{uuid}` mechanism as `bom upload`) until
  processing finishes and reports the resulting cloned project (uuid, name,
  version) instead of the bare token. `--no-wait` skips polling and reports
  only the token.
- `--json` and `--output-uuid` flags on `project get`, `project search`, and
  `project clone` for scripting: `--json` prints the full result as JSON
  (for `clone`, including the resolved cloned project once polling
  completes), `--output-uuid` prints only the relevant uuid (for `clone`,
  the cloned project's uuid, or the token with `--no-wait`) and nothing
  else. The two are mutually exclusive.
- `project delete` command: deletes a single project, identified either by
  `--by-uuid` or by `--project-name`/`--version` (mutually exclusive), with
  a confirmation prompt skippable via `--yes`.
- `project bom upload <bom-file>` command: uploads a CycloneDX BOM to a
  project identified either by `--name`/`--version` (with `--auto-create`
  and `--parent-name`/`--parent-version`/`--parent-uuid` for creating it
  under a parent) or, mutually exclusively, directly by `--by-uuid`.
  Supports `--is-latest`. Upload is asynchronous server-side, so the command
  reports the tracking token and polls until processing completes (or
  `--no-wait` to skip that and return immediately). `--skip-if-inactive`
  looks the project up first and skips the upload with a warning (exit code
  `0`) if it already exists and is inactive; a not-yet-existing project
  (with `--auto-create`) is never treated as inactive.
- Connection settings loaded from `~/.dtrack/config.yaml` (`url`, `api-key`),
  optionally overridden (or supplied entirely, making the file unnecessary)
  via the `DT_BASE_URL` and `DT_API_KEY` environment variables.
- `--insecure` global flag to disable TLS verification.
- Test suite covering the cleanup flow and the config loader.
- GitHub Actions CI workflow (gofmt, vet, race tests, build) and a release
  workflow that cross-compiles binaries for Linux, macOS, and Windows
  (amd64/arm64) and attaches them, with checksums, to tagged GitHub Releases.
- Build-time version injection via `-ldflags -X main.version`, surfaced through
  `dtrack --version`.

### Fixed

- `project children deactivate` no longer errors when a matched child is
  already inactive. Dependency-Track's `PATCH /v1/project/{uuid}` responds
  `304 Not Modified` (not `200`) when a deactivate request wouldn't change
  anything; the client now treats that as success instead of a failure.
