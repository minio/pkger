# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

pkger is a packaging tool for MinIO projects that generates DEB, RPM, and APK packages along with installation metadata JSON files. It's built in Go as a single-file application (`main.go`) that uses the nfpm library for package generation.

## Building and Running

Build the project:
```bash
go build -o pkger main.go
```

The binary is self-contained and uses command-line flags for all configuration.

## Core Architecture

### Single-file Design
The entire application is in `main.go` (~1000 lines). Key components:
- **Command-line parsing**: Uses `kingpin` for flag handling
- **Package generation**: Uses `goreleaser/nfpm/v2` library with support for deb, rpm, and apk formats
- **Template system**: Uses Go's `text/template` for generating nfpm config (lines 88-119)
- **JSON generation**: Creates download metadata files for different applications and platforms

### Application Types
pkger supports multiple MinIO applications, each with different versioning and architecture requirements:

1. **minio/mc**: Date-based releases (e.g., `RELEASE.2025-03-12T00-00-00Z`)
   - Supports: amd64, arm64, ppc64le
   - Generates packages and cross-platform download metadata

2. **minio-enterprise/mc-enterprise**: Enterprise variants (date-based)
   - Supports: amd64, arm64 only
   - Generates AIStor-branded download URLs

3. **sidekick**: Load balancer (date-based releases)
   - Supports: amd64, arm64 only
   - Generates package-only metadata (no binary downloads)

4. **warp**: Benchmarking tool (semantic versioning, e.g., `v0.4.3`)
   - Supports: amd64, arm64 only
   - Strips 'v' prefix from package filenames
   - Cross-platform: Linux, macOS (arm64), Windows (amd64)

### Version Handling
- **Date-based** (`RELEASE.2025-03-12T00-00-00Z`): Converted to semver format `20250312000000.0.0` via `semVerRelease()` (lines 793-806)
- **Semantic** (`v0.4.3` for warp): Validated and 'v' prefix stripped (lines 731-744)

### Package Generation Flow
1. Parse release tag and convert to appropriate version format
2. For each architecture (filtered by app requirements):
   - Generate nfpm config from template (lines 865-920)
   - Create packages in `{appName}-release/linux-{arch}/` directory
   - Generate SHA256 checksums for each package
   - Create symlinks for latest package
3. Generate `downloads-{appName}.json` metadata file

## Common Commands

Run pkger for minio (date-based release):
```bash
# Requires: minio.service file and binaries in dist/linux-{arch}/
pkger -r RELEASE.2025-03-12T00-00-00Z --appName minio --releaseDir=dist
```

Run pkger for sidekick:
```bash
# Requires: binaries in sidekick-release/linux-{arch}/
pkger -r RELEASE.2025-03-12T00-00-00Z --appName sidekick
```

Run pkger for warp (semantic versioning):
```bash
# Requires: binaries in warp-release/linux-{arch}/
pkger -r v0.4.3 --appName warp
```

Build specific package formats only:
```bash
pkger -r <release> --appName <app> --packager deb,rpm
```

Ignore missing architectures (continue on errors):
```bash
pkger -r <release> --appName <app> --ignore
```

Skip package building (only generate JSON):
```bash
pkger -r <release> --appName <app> --no-pkg
```

Generate EDGE release (uses /edge/ path instead of /release/):
```bash
pkger -r RELEASE.2025-03-12T00-00-00Z --appName minio-enterprise --edge --no-pkg
```

## Key Flags

- `-r, --release`: Release tag (required). Format depends on app type
- `-a, --appName`: Application name (default: "minio")
- `-d, --releaseDir`: Directory containing binaries (default: "{appName}-release")
- `-p, --packager`: Package formats to build (default: "deb,rpm,apk")
- `-i, --ignore`: Ignore missing architecture errors
- `-n, --no-pkg`: Skip package generation
- `-e, --edge`: Generate EDGE release URLs (uses /edge/ path)
- `-s, --scriptsDir`: Directory with package scripts (preinstall.sh, postinstall.sh, etc.)
- `-l, --license`: Package license (default: "AGPLv3")
- `--deps`: JSON file with package dependencies

## Important Implementation Details

### Architecture Mapping
- RPM uses x86_64/aarch64 (see `rpmArchMap`, lines 150-153)
- DEB uses amd64/arm64 (see `debArchMap`, lines 155-158)

### Download URL Patterns
The JSON generators create download metadata with different URL structures:
- **Community**: `dl.min.io/{server|client}/{appName}/release/...`
- **Enterprise Release**: `dl.min.io/aistor/{appName}/release/...`
- **Enterprise EDGE**: `dl.min.io/aistor/{appName}/edge/...` (with `--edge` flag)
- **Sidekick/Warp**: Always use `dl.min.io/aistor/` path

### EDGE Release Support
- Use `--edge` flag to generate EDGE release metadata
- Changes URL path from `/release/` to `/edge/`
- Generates separate JSON file: `downloads-{appName}-edge.json`
- Docker/Podman instructions use release tag (not `:latest`)
- Package building (RPM/DEB) works the same for both release and EDGE

### Special Cases
- **minio/aistor**: Includes `minio.service` systemd file in packages (lines 103-106)
- **mc packages**: Binary named "mc" but package name is "mcli" (lines 870-872)
- **warp**: Version validation enforces `vX.Y.Z` format (lines 734-742)
- **Docker tags**: All Docker/Podman instructions now use actual release tags instead of `:latest`

## Testing Changes

When modifying version handling or JSON generation:
1. Test with all app types: minio, sidekick, warp, minio-enterprise
2. Verify package filenames match conventions (no 'v' prefix for warp)
3. Check generated JSON URLs point to correct dl.min.io paths
4. Ensure architecture filtering works correctly for each app

## File Structure

```
pkger/
├── main.go              # Single-file application
├── go.mod               # Go 1.25+ required
├── minio.service        # Systemd service file (included in minio packages)
├── dist/                # GoReleaser output for pkger itself
└── {app}-release/       # Input/output directories for packaging
    └── linux-{arch}/
        ├── {binary}.{release}        # Input binary
        ├── {package}.rpm             # Output package
        ├── {package}.deb
        ├── {package}.apk
        └── *.sha256sum               # Checksums
```

## Testing

Run unit tests:
```bash
go test -v
```

The test suite (`main_test.go`) covers:
- Version conversion (date-based and semantic)
- JSON generation for all app types
- EDGE release URL validation
- Docker tag usage verification
- Architecture mapping correctness
- URL path structure validation

## Development Notes

- Comprehensive unit tests exist in `main_test.go` covering all JSON generation functions
- Package scripts (preinstall.sh, etc.) are optional and loaded from `--scriptsDir`
- The template system expects specific directory structures; paths are not validated upfront
- JSON generation happens regardless of package build success/failure
