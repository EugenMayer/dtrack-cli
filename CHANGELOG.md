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
- `project search <name>` command: looks up every version of a project by
  exact name, with `--version` to narrow to one version and `--only-active`
  to exclude inactive projects.
- `project clone <name>[@source-version] <new-version>` command: clones a
  project into a new version, with `--include-tags`, `--include-properties`,
  `--include-dependencies`, `--include-components`, `--include-services`,
  `--include-audit-history`, `--include-acl`, `--include-policy-violations`,
  and `--make-clone-latest` flags mirroring Dependency-Track's clone options.
  Cloning is asynchronous server-side, so the command reports the tracking
  token Dependency-Track returns.
- `--json` and `--output-uuid` flags on `project search` and `project clone`
  for scripting: `--json` prints the full result as JSON, `--output-uuid`
  prints only the relevant uuid(s)/token and nothing else. The two are
  mutually exclusive.
- `project bom upload <bom-file>` command: uploads a CycloneDX BOM to a
  project identified either by `--name`/`--version` (with `--auto-create`
  and `--parent-name`/`--parent-version`/`--parent-uuid` for creating it
  under a parent) or, mutually exclusively, directly by `--by-uuid`.
  Supports `--is-latest`. Upload is asynchronous server-side, so the command
  reports the tracking token and polls until processing completes (or
  `--no-wait` to skip that and return immediately).
- Connection settings loaded from `~/.dtrack/config.yaml` (`url`, `api-key`).
- `--insecure` global flag to disable TLS verification.
- Test suite covering the cleanup flow and the config loader.
- GitHub Actions CI workflow (gofmt, vet, race tests, build) and a release
  workflow that cross-compiles binaries for Linux, macOS, and Windows
  (amd64/arm64) and attaches them, with checksums, to tagged GitHub Releases.
- Build-time version injection via `-ldflags -X main.version`, surfaced through
  `dtrack --version`.
