# Repository Guidelines

## Project Structure & Module Organization

`jkv` is a Go 1.24 CLI version manager for JVM tools. `cmd/jkv/` contains the executable entry and command dispatch. `internal/catalog/` discovers releases from Chinese mirrors; `internal/store/` downloads, verifies, extracts, and tracks installations. Tests sit beside packages as `*_test.go`. Source rationale belongs in `docs/sources.md`; installers are `install.sh` and `install.ps1`; automation lives under `.github/workflows/`.

## Build, Test, and Development Commands

- `go run ./cmd/jkv list java` runs the CLI from source and exercises live catalog discovery.
- `go build ./cmd/jkv` builds the local executable.
- `go test ./...` runs all unit and live mirror tests.
- `go test -short ./...` skips network-dependent tests such as `TestDragonwellLive`.
- `go vet ./...` performs the static checks required by CI.
- `gofmt -w cmd internal` formats Go sources before submission.
- `./install.sh` or `.\install.ps1` builds and installs from a source checkout.

CI also cross-compiles with `CGO_ENABLED=0` for Linux, macOS, and Windows on `amd64` and `arm64`. Keep code portable across all six targets.

## Coding Style & Naming Conventions

Follow standard Go formatting and idioms. Use tabs emitted by `gofmt`; keep package names lowercase, exported identifiers in `PascalCase`, and unexported identifiers in `camelCase`. Return contextual errors instead of swallowing failures. Preserve existing Chinese user-facing CLI text. Keep provider discovery in `internal/catalog` and filesystem or archive behavior in `internal/store`.

## Testing Guidelines

Use Go's `testing` package. Name files `*_test.go` and tests `TestBehavior`, for example `TestUnzipRejectsTraversal`. Prefer table-driven tests for version and platform cases, and `t.TempDir()` for filesystem isolation. Network tests must honor `testing.Short()`. No coverage threshold is configured; every bug fix and new command path should receive a focused regression test.

## Commit & Pull Request Guidelines

History follows Conventional Commit-style subjects such as `feat: add ...` and `ci: upgrade ...`. Use a short imperative subject with an accurate scope (`fix:`, `docs:`, `test:`, or `ci:`). Pull requests should explain motivation and behavior changes, link relevant issues, list commands run, and include representative CLI output when user-visible behavior changes. Ensure tests, vetting, and cross-platform assumptions are addressed before review.

## Security & Configuration Tips

Test installation changes with a temporary `JKV_DIR`. Preserve SHA-256 verification where upstream checksums exist, archive path-traversal defenses, HTTPS sources, and no-overwrite behavior for existing Maven or Gradle configuration.
