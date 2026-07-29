# jkv

[![CI](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml/badge.svg)](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml)

A China-network-friendly, cross-platform version manager for JVM tools. It discovers releases from public official Chinese mirrors, verifies downloads when upstream checksums exist, installs side-by-side versions, and switches versions per shell or project.

v0.2 is beta. Eclipse Temurin, Maven, and Gradle are core-supported. Other providers are beta.

## Install

Release notes provide the configured domestic URLs for `install.sh` and `install.ps1`. The installer downloads jkv itself from domestic object storage first and falls back to GitHub only after a transfer failure. A checksum mismatch is terminal and never triggers fallback.

Tool artifacts—JDKs, Maven, Gradle, and others—continue to come from their public official Chinese mirrors.

```sh
curl -fsSL <domestic-script-url>/install.sh | sh
```

```powershell
irm <domestic-script-url>/install.ps1 | iex
```

Supported shells: Bash, Zsh, Fish, and PowerShell. Supported targets: Linux, macOS, and Windows on amd64 and arm64 where the selected provider has an artifact.

## Usage

```sh
jkv list
jkv list java
jkv install java 21-tem
jkv install maven
jkv use java 21-tem
jkv default java 21-tem
jkv env init
jkv doctor
```

Machine consumers may pass `--json`. Stable exit categories are documented in [docs/commands.md](docs/commands.md).

See [source policy](docs/sources.md), [support policy](docs/support.md), [troubleshooting](docs/troubleshooting.md), [security policy](SECURITY.md), and [contributing guide](CONTRIBUTING.md).
