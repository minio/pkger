# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

pkger is a packaging tool for MinIO/AIStor projects that generates DEB, RPM, and APK packages along with the download metadata JSON consumed by min.io/download. It's built in Go as a single-file application (`main.go`) that uses the nfpm library for package generation.

## Building and Running

```bash
go build -o pkger .
```

The binary is self-contained and uses command-line flags for all configuration.

## Core Architecture

### Single-file Design

The entire application is in `main.go`. Key components:

- **Command-line parsing**: `kingpin` for flag handling
- **Package generation**: `goreleaser/nfpm/v2`, supporting deb, rpm and apk
- **Template system**: Go `text/template` renders an nfpm config per architecture (`const tmpl`)
- **JSON generation**: download metadata for min.io/download

### Supported Apps

Every app packages for **`linux/amd64` and `linux/arm64` only** — see `pkgArches`. There is no ppc64le, and no community `minio`/`mc` app; those were removed along with the last ppc64le build.

| `--appName` | Release dir        | Binary read | Package name | Versioning | Metadata                                     |
| ----------- | ------------------ | ----------- | ------------ | ---------- | -------------------------------------------- |
| `aistor`    | `minio-release`    | `minio`     | `minio`      | date-based | `downloads-aistor.json` (AIStor + MinIO KMS) |
| `ac`        | `mc-release`       | `mc`        | `mcli`       | date-based | `downloads-ac.json` (AIStor Client)          |
| `sidekick`  | `sidekick-release` | `sidekick`  | `sidekick`   | date-based | `downloads-sidekick.json` (Linux + Windows)  |
| `warp`      | `warp-release`     | `warp`      | `warp`       | semver     | `downloads-warp.json` (cross-platform)       |
| `memkv`     | `memkv-release`    | `memkv`     | `memkv`      | date-based | empty document                               |
| `aimem`     | `aimem-release`    | `aimem`     | `aimem`      | date-based | empty document                               |
| `minfs`     | `minfs-release`    | `minfs`     | `minfs`      | date-based | empty document                               |

`aistor` and `ac` were renamed from `minio-enterprise` and `mc-enterprise`. **Only the app name changed.** The release directory, the binary read from it, the package name, and every `dl.min.io` path segment are deliberately unchanged, so published artifacts are byte-identical across the rename. When touching `releaseDirName`, `defaultPkgName` or `defaultBinarySrcName`, re-key the switch on the new app name but keep the returned on-disk name.

`memkv`, `aimem` and `minfs` fall through to `generateDownloadsJSON`, which intentionally returns an empty document — the packages ship, but these apps have no dl.min.io download page to link.

### Version Handling

- **Date-based** (`RELEASE.2025-03-12T00-00-00Z`): converted to semver `20250312000000.0.0` via `semVerRelease()`
- **Semantic** (`v0.4.3`, warp only): validated against `vX.Y.Z` and the `v` prefix stripped for package filenames

### Package Generation Flow

1. Parse the release tag and convert it to the appropriate version format
2. For each architecture in `pkgArches`:
   - Render the nfpm config from the template
   - Write packages to `{releaseDir}/linux-{arch}/`
   - Write a `.sha256sum` beside each package
   - Symlink a stable "latest" alias (e.g. `minio.deb`), plus the legacy filenames when `--package-name` renamed the package
3. Write `downloads-{appName}.json` (or `downloads-{appName}-edge.json` with `--edge`)

## Common Commands

```bash
# aistor (date-based); needs minio.service and binaries in dist/linux-{arch}/
pkger -r RELEASE.2025-03-12T00-00-00Z --appName aistor --releaseDir=dist

# ac client
pkger -r RELEASE.2025-03-12T00-00-00Z --appName ac

# sidekick
pkger -r RELEASE.2025-03-12T00-00-00Z --appName sidekick

# warp (semantic versioning)
pkger -r v0.4.3 --appName warp

# specific package formats only
pkger -r <release> --appName <app> --packager deb,rpm

# continue past missing architectures
pkger -r <release> --appName <app> --ignore

# JSON metadata only
pkger -r <release> --appName <app> --no-pkg

# EDGE release (uses /edge/ instead of /release/)
pkger -r EDGE.2025-03-12T00-00-00Z --appName aistor --edge --no-pkg

# rename the installed binary/package (see README)
pkger -r <release> --appName aistor --binary-name aistor --package-name aistor
```

## Key Flags

- `-r, --release`: release tag (required); format depends on the app
- `-a, --appName`: application name (default: `aistor`)
- `-d, --releaseDir`: directory containing binaries (default: per-app, see table)
- `-p, --packager`: formats to build (default: `deb,rpm,apk`)
- `-i, --ignore`: ignore missing architecture errors
- `-n, --no-pkg`: skip package generation
- `-j, --no-json`: skip JSON metadata generation
- `-e, --edge`: EDGE release URLs (`/edge/` path)
- `-s, --scriptsDir`: directory with package scripts (preinstall.sh, postinstall.sh, preremove.sh, postremove.sh)
- `-l, --license`: package license (default: `AGPLv3`)
- `-c, --contents`: YAML file with extra nfpm content entries (supports `${ARCH}`)
- `--deps`: JSON file with per-format package dependencies
- `--binary-name` / `--package-name`: override the source binary base name and the package/installed-command name; see README

## Important Implementation Details

### Architecture Mapping

- RPM uses x86_64/aarch64 (`rpmArchMap`)
- DEB uses amd64/arm64 (`debArchMap`)

### Download URL Patterns

`dl.min.io` path segments are **hardcoded literals**, never interpolated from `--appName`. Renaming an app must not change a published URL.

- **AIStor server**: `dl.min.io/aistor/minio/{release,edge}/...`
- **AIStor client**: `dl.min.io/aistor/mc/{release,edge}/...`
- **MinIO KMS**: `dl.min.io/aistor/minkms/{release,edge}/...` (emitted alongside aistor)
- **Sidekick / warp**: `dl.min.io/aistor/{sidekick,warp}/release/...`
- One macOS Homebrew entry still points at `dl.min.io/server/minio/...`

### EDGE Release Support

- `--edge` switches the URL path from `/release/` to `/edge/` and writes `downloads-{appName}-edge.json`
- An `EDGE.`-prefixed release tag requires `--edge`, and `--edge` rejects a `RELEASE.`-prefixed tag
- Package building works identically for release and EDGE
- Docker/Podman instructions use the actual release tag, not `:latest`

### Special Cases

- **aistor**: includes a `minio.service` systemd unit, gated on the _binary_ name being `minio` or `aistor`. The file is **not** in this repo — supply it in the working directory (q downloads it via the `service_file` config).
- **sidekick**: includes `sidekick.service` from this repo
- **ac**: binary `mc`, package `mcli`
- **warp**: enforces `vX.Y.Z`
- **`--package-name` rename**: when it differs from the app default, the old name becomes a legacy name. Inside the package that means a `/usr/local/bin/<old> -> <new>` symlink plus `provides`/`replaces`/`conflicts`. In the release dir it also means the old package filename, the old "latest" alias and the old `.sha256sum` are symlinked onto the new package, so already-published download URLs keep resolving. `TestPackageRenameKeepsOldLinks` pins this; `TestPackageDefaultsEmitNoLegacyLinks` pins that the non-rename path emits none of it.

## Testing

```bash
go test ./...
```

`main_test.go` covers:

- Version conversion (date-based and semantic)
- The nfpm template under both default and renamed (`--binary-name`/`--package-name`) paths
- JSON generation for aistor, ac, sidekick and warp, plus the empty fallback document
- EDGE URL structure and Docker tag usage
- Architecture mapping and the `pkgArches` set
- Per-app release directory resolution

When modifying version handling or JSON generation:

1. Test every app in the table above
2. Verify package filenames match convention (no `v` prefix for warp; `minio`/`mcli` for aistor/ac)
3. Check generated JSON URLs still point at the correct dl.min.io paths
4. Confirm no new architecture leaked into `pkgArches`

## Downstream Consumers

`--appName`, the release directory layout, and the `downloads-{appName}.json` filename are a contract with the `q` release automation repo:

- `q/v3/internal/pipeline/stage_build.go` builds the pkger argv from each product's `.qreleaser.yml` `packaging.app_name`
- `q/lib/copy-aistor-release-assets.sh` and `q/community/binary-releases/copy-release-assets.sh` look the JSON up by filename
- The product repos' `.qreleaser.yml` (`aistor`, `ac`, …) set `app_name`

Renaming an app or changing the JSON filename requires coordinated edits there. `q/legacy-v1/` is archived and intentionally left on the old names.

## File Structure

```text
pkger/
├── main.go              # Single-file application
├── main_test.go         # Unit tests
├── go.mod               # Go 1.26+ required
├── sidekick.service     # Systemd unit shipped in sidekick packages
├── dist/                # GoReleaser output for pkger itself
└── {app}-release/       # Input/output directories for packaging
    └── linux-{arch}/
        ├── {binary}.{release}        # Input binary
        ├── {package}.{rpm,deb,apk}   # Output packages
        ├── {alias}.{rpm,deb,apk}     # "Latest" symlink
        └── *.sha256sum               # Checksums
```

## Development Notes

- Package scripts (preinstall.sh, etc.) are optional and loaded from `--scriptsDir`
- The template system expects specific directory structures; paths are not validated upfront
- JSON generation happens regardless of package build success/failure
- Avoid citing `main.go` line numbers in docs — they rot; name the function or variable instead
