# jkv

A China-network-friendly, cross-platform version manager for JVM tools. Install, switch, and pin Java, Maven, Gradle, and more with one CLI.

[中文](README.md) · [command reference](docs/commands.md) · [troubleshooting](docs/troubleshooting.md) · [security](SECURITY.md)

## Quick start

```sh
curl -fsSL https://cnb.cool/fishandsheep/jkv/-/releases/download/v0.3.0-beta.2/install.sh | sh
```

```powershell
irm https://cnb.cool/fishandsheep/jkv/-/releases/download/v0.3.0-beta.2/install.ps1 | iex
```

The installer prefers CNB and falls back to GitHub only after a transfer failure; a checksum mismatch is terminal. Or download a binary from [Releases](https://github.com/fishandsheep/jkv/releases/latest), or run `go install github.com/fishandsheep/jkv/cmd/jkv@v0.3.0-beta.2`.

Load a shell hook before switching versions:

```sh
eval "$(jkv init zsh)"
jkv install java 21-tem
jkv use java 21-tem
```

## Everyday commands

```sh
jkv list java             # alias: jkv ls java
jkv install java 21-tem   # alias: jkv i
jkv use java 21-tem       # alias: jkv u; current shell
jkv default java 21-tem   # alias: jkv d; new shells
jkv current               # alias: jkv c
jkv doctor
```

Use `jkv env init` and `jkv env apply` to commit a project `.jkvrc`. Maven and Gradle dependency mirrors are configured separately with `jkv mirror <maven|gradle> --apply`.

## Catalog

jkv manages local downloads and installations. [jkv-catalog](https://github.com/fishandsheep/jkv-catalog) reviews versions, platforms, URLs, and checksums. v0.3 consumes signed catalog snapshots while never downloading or executing remote provider code. See [Catalog usage](docs/catalog.md).

For all commands, JSON output, exit codes, contribution guidance, and source policy, start from the [Chinese README](README.md).
