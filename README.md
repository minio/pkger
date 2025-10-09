## pkger

pkger is a packaging tool for MinIO projects that generates DEB, RPM, and APK packages along with download metadata JSON files consumed by min.io/download.

## Packaging minio during development

For testing minio packages during development, first install pkger so it's available in your PATH. Then prepare a release directory (such as `dist`) with architecture-specific subdirectories. For example, create `./dist/linux-amd64` and move your compiled minio binary there, renaming it to include the release version like `minio.RELEASE.2025-03-12T00-00-00Z.debug.GIT_TAG`. Make sure to replace the timestamp and git tag with your actual values.

You'll also need the minio.service systemd file, which you can download from the minio-service repository:

```shell
wget -O minio.service "https://raw.githubusercontent.com/minio/minio-service/refs/heads/master/linux-systemd/minio.service"
```

Then run pkger with the release version, specifying minio as the app name and using the `--ignore` flag to continue even if some architectures are missing:

```shell
pkger -r RELEASE.2025-03-12T00-00-00Z.debug.GIT_TAG --appName minio --ignore --releaseDir=dist
```

The packaged files (rpm, deb, apk) along with the downloads JSON metadata will be generated in the `./dist` directory.

## Packaging sidekick releases

Sidekick releases follow a similar workflow. Create the release directory structure with subdirectories for each supported architecture (amd64 and arm64 only). Place your compiled sidekick binaries in these directories with the release version appended to the filename. Note that sidekick only supports amd64 and arm64 architectures—ppc64le is not included.

```shell
mkdir -p ./sidekick-release/linux-amd64 ./sidekick-release/linux-arm64
mv ./sidekick-linux-amd64 ./sidekick-release/linux-amd64/sidekick.RELEASE.2025-03-12T00-00-00Z
mv ./sidekick-linux-arm64 ./sidekick-release/linux-arm64/sidekick.RELEASE.2025-03-12T00-00-00Z
```

Run pkger with the sidekick app name. By default, it builds all three package formats (RPM, DEB, APK), though you can limit this with the `--packager` flag:

```shell
pkger -r RELEASE.2025-03-12T00-00-00Z --appName sidekick
```

The generated packages will appear in the architecture-specific directories along with `downloads-sidekick.json`, which contains download URLs and installation instructions. Note that while APK packages are built, only RPM and DEB installation instructions are included in the JSON metadata.

## Packaging warp releases

Warp uses semantic versioning (e.g., v0.4.3) instead of date-based release tags. The version must include the `v` prefix when you run pkger, but this prefix is automatically stripped in the generated package filenames to follow standard RPM and DEB naming conventions.

Set up the release directories for amd64 and arm64 (warp doesn't support ppc64le):

```shell
mkdir -p ./warp-release/linux-amd64 ./warp-release/linux-arm64
mv ./warp-linux-amd64 ./warp-release/linux-amd64/warp.v0.4.3
mv ./warp-linux-arm64 ./warp-release/linux-arm64/warp.v0.4.3
```

Run pkger with the semantic version. The version must start with `v` and follow the X.Y.Z format:

```shell
pkger -r v0.4.3 --appName warp
```

The output includes Linux packages (RPM, DEB, APK) in the architecture directories, along with `downloads-warp.json`. This JSON file includes cross-platform download information for Linux (binary, RPM, DEB), macOS (arm64 binary only), and Windows (amd64 binary). Like sidekick, APK packages are built for Linux but only RPM and DEB installation instructions are documented in the JSON.
