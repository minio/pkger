## pkger

## Packaging minio during development (for testing, etc)

1. First install pkger so it is available in PATH.

2. Prepare the release directory, `dist` below:

```shell
mkdir -p ./dist/linux-amd64

# REPLACE THE TIMESTAMP AND THE GIT TAG!
VERSION=RELEASE.2025-03-12T00-00-00Z.debug.GIT_TAG

# Move the binary to the dist directory
mv ./minio ./dist/linux-amd64/minio.$VERSION
```

3. Ensure minio.service exists:

```
wget -O minio.service "https://raw.githubusercontent.com/minio/minio-service/refs/heads/master/linux-systemd/minio.service"
```

4. Run pkger:

```shell
pkger -r $VERSION --appName minio --ignore --releaseDir=dist
```

5. `./dist` will contain the packaged files (rpm, deb, etc).

## Packaging sidekick releases

1. First install pkger so it is available in PATH.

2. Prepare the release directory:

```shell
mkdir -p ./sidekick-release/linux-amd64
mkdir -p ./sidekick-release/linux-arm64

# REPLACE THE TIMESTAMP!
VERSION=RELEASE.2025-03-12T00-00-00Z

# Move the binaries to the release directory
mv ./sidekick-linux-amd64 ./sidekick-release/linux-amd64/sidekick.$VERSION
mv ./sidekick-linux-arm64 ./sidekick-release/linux-arm64/sidekick.$VERSION
```

3. Run pkger:

```shell
pkger -r $VERSION --appName sidekick
```

4. The output will be:
   - **Packages**: `sidekick-release/linux-{arch}/sidekick-*.rpm`, `sidekick-*.deb`, `sidekick-*.apk`
   - **JSON metadata**: `sidekick-release/downloads-sidekick.json` (contains download URLs and installation instructions)

**Notes:**
- Sidekick packages are built for `amd64` and `arm64` architectures only (no `ppc64le`)
- By default, all three package formats (RPM, DEB, APK) are built
- Use `--packager` flag to build specific formats: `--packager deb,rpm`
- The downloads JSON includes only RPM and DEB installation instructions (APK is built but not documented)
