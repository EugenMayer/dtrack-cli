# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `project children cleanup` command: interactively (or via flags) selects a
  collection project, aggregates its direct children matching a given version,
  shows an overview, and deletes them on confirmation.
- Flags for the cleanup command: `--collection`, `--version`,
  `--include-inactive`, `--dry-run`, `--yes`.
- Connection settings loaded from `~/.dtrack/config.yaml` (`url`, `api-key`).
- `--insecure` global flag to disable TLS verification.
- Test suite covering the cleanup flow and the config loader.
