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

5. `./dist will contain the packaged files (rpm, deb, etc).
