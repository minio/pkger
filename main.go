package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/alecthomas/kingpin"
	jsoniter "github.com/json-iterator/go"
	sha256 "github.com/minio/sha256-simd"

	"github.com/goreleaser/nfpm/v2"
	_ "github.com/goreleaser/nfpm/v2/apk"
	_ "github.com/goreleaser/nfpm/v2/deb"
	_ "github.com/goreleaser/nfpm/v2/rpm"
)

// nolint: gochecknoglobals
var (
	version        = "v1.0"
	releaseMatcher = regexp.MustCompile(`[0-9]`)

	app     = kingpin.New("pkger", "Debian, RPMs and APKs for MinIO")
	appName = app.Flag("appName", "Application name for the package").
		Default("minio").
		Short('a').
		String()
	release = app.Flag("release", "Current release tag").
		Default("").
		Short('r').
		String()
	packager = app.Flag("packager", "Select packager implementation to use, defaults to: `deb,rpm,apk`").
			Default("deb,rpm,apk").
			Short('p').
			Enum("deb", "rpm", "apk", "deb,rpm,apk")
)

const tmpl = `name: "{{ .App }}"
arch: "{{ .Arch }}"
platform: "{{ .OS }}"
version: "{{ .SemVerRelease }}"
maintainer: "MinIO Development <dev@minio.io>"
description: |
  MinIO is a High Performance Object Storage released under Apache License v2.0.
  It is API compatible with Amazon S3 cloud storage service. Use MinIO to build
  high performance infrastructure for machine learning, analytics and application
  data workloads.
vendor: "MinIO, Inc."
homepage: "https://min.io"
license: "Apache v2.0"
contents:
- src: {{ .App }}-release/{{ .OS }}-{{ .Arch }}/{{ .App }}.{{ .Release }}
  dst: /usr/local/bin/{{ .App }}
`

const dlURLPrefix = "https://dl.minio.io/server/minio/release/"

type downloadJSON struct {
	Bin          string `json:"Bin"`
	BinText      string `json:"BinText"`
	BinCksum     string `json:"BinCksum"`
	RPM          string `json:"RPM,omitempty"`
	RPMCksum     string `json:"RPMCksum,omitempty"`
	RPMText      string `json:"RPMText,omitempty"`
	Deb          string `json:"DEB,omitempty"`
	DebCksum     string `json:"DEBCksum,omitempty"`
	DebText      string `json:"DEBText,omitempty"`
	HomebrewText string `json:"HomebrewText,omitempty"`
}

type downloadsJSON struct {
	Linux   map[string]map[string]downloadJSON `json:"Linux"`
	MacOS   map[string]map[string]downloadJSON `json:"MacOS"`
	Windows map[string]map[string]downloadJSON `json:"Windows"`
}

var rpmArchMap = map[string]string{
	"amd64":   "x86_64",
	"ppc64le": "ppc64le",
	"s390x":   "s390x",
	"arm64":   "aarch64",
}

var debArchMap = map[string]string{
	"amd64":   "amd64",
	"s390x":   "s390x",
	"arm64":   "arm64",
	"ppc64le": "ppc64el",
}

func generateDownloadsJSON(semVerTag string) downloadsJSON {
	d := downloadsJSON{
		Linux:   make(map[string]map[string]downloadJSON),
		MacOS:   make(map[string]map[string]downloadJSON),
		Windows: make(map[string]map[string]downloadJSON),
	}
	for _, linuxArch := range []string{
		"amd64",
		"arm64",
		"s390x",
		"ppc64le",
	} {
		d.Linux["MinIO Server"] = map[string]downloadJSON{
			linuxArch: downloadJSON{
				Bin: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio", linuxArch),
				BinText: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio
chmod +x minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password ./minio server /mnt/data`, linuxArch),
				BinCksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio.sha256sum", linuxArch),
				RPM:      fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
				RPMCksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
				RPMText: fmt.Sprintf(`dnf install https://dl.min.io/server/minio/release/linux-%s/minio-%s.%s.rpm
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				Deb:      fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
				DebCksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
				DebText: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb
dpkg -i minio_%s_%s.deb
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
			},
		}
		d.Linux["MinIO Client"] = map[string]downloadJSON{
			linuxArch: downloadJSON{
				Bin: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc", linuxArch),
				BinText: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/linux-%s/mc
chmod +x mc
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch),
				BinCksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc.sha256sum", linuxArch),
				RPM:      fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc-%s.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
				RPMCksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc-%s.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
				RPMText: fmt.Sprintf(`dnf install https://dl.min.io/client/mc/release/linux-%s/mc-%s.%s.rpm
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				Deb:      fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
				DebCksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
				DebText: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/linux-%s/mc_%s_%s.deb
dpkg -i minio_%s_%s.deb
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
			},
		}
	}
	for _, macArch := range []string{
		"amd64",
	} {
		d.MacOS["MinIO Server"] = map[string]downloadJSON{
			macArch: downloadJSON{
				HomebrewText: `brew install minio/stable/minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data`,
				Bin:      fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio", macArch),
				BinCksum: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio.sha56sum", macArch),
				BinText: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/darwin-%s/minio
chmod +x minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password ./minio server /mnt/data`, macArch),
			},
		}
		d.MacOS["MinIO Client"] = map[string]downloadJSON{
			macArch: downloadJSON{
				HomebrewText: `brew install minio/stable/mc
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`,
				Bin:      fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc", macArch),
				BinCksum: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc.sha56sum", macArch),
				BinText: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/darwin-%s/mc
chmod +x mc
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, macArch),
			},
		}
	}
	for _, winArch := range []string{
		"amd64",
	} {
		d.Windows["MinIO Server"] = map[string]downloadJSON{
			winArch: downloadJSON{
				Bin: fmt.Sprintf("https://dl.min.io/server/minio/release/windows-%s/minio.exe", winArch),
				BinText: fmt.Sprintf(`PS> Invoke-WebRequest -Uri "https://dl.min.io/server/minio/release/windows-%s/minio.exe" -OutFile "C:\minio.exe"
PS> setx MINIO_ROOT_USER admin
PS> setx MINIO_ROOT_PASSWORD password
PS> C:\minio.exe server F:\Data`, winArch),
				BinCksum: fmt.Sprintf("https://dl.min.io/server/minio/release/windows-%s/minio.exe.sha56sum", winArch),
			},
		}
		d.Windows["MinIO Client"] = map[string]downloadJSON{
			winArch: downloadJSON{
				Bin: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe", winArch),
				BinText: fmt.Sprintf(`PS> Invoke-WebRequest -Uri "https://dl.minio.io/client/mc/release/windows-amd64/mc.exe" -OutFile "C:\mc.exe"
C:\mc.exe alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, winArch),
				BinCksum: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe.sha56sum", winArch),
			},
		}
	}
	return d
}

func main() {
	app.Version(version)
	app.VersionFlag.Short('v')
	app.HelpFlag.Short('h')
	app.Parse(os.Args[1:])
	if err := doPackage(*appName, *release, *packager); err != nil {
		kingpin.Fatalf(err.Error())
	}
}

var errInsufficientParams = errors.New("a packager must be specified if output is a directory or blank")

type releaseTmpl struct {
	App           string
	OS            string
	Arch          string
	Release       string
	SemVerRelease string
}

func semVerRelease(release string) string {
	return "0.0." + strings.Join(releaseMatcher.FindAllString(release, -1), "")
}

// nolint:funlen
func doPackage(appName, release, packager string) error {
	mtmpl, err := template.New("minio").Parse(tmpl)
	if err != nil {
		return err
	}

	semVerTag := semVerRelease(release)
	d := generateDownloadsJSON(semVerTag)
	for _, arch := range []string{
		"amd64",
		"arm64",
		"s390x",
		"ppc64le",
	} {
		var buf bytes.Buffer
		err = mtmpl.Execute(&buf, releaseTmpl{
			App:           appName,
			OS:            "linux",
			Arch:          arch,
			Release:       release,
			SemVerRelease: semVerTag,
		})

		config, err := nfpm.Parse(&buf)
		if err != nil {
			return err
		}

		for _, pkger := range strings.Split(packager, ",") {
			info, err := config.Get(pkger)
			if err != nil {
				return err
			}

			info = nfpm.WithDefaults(info)

			if err = nfpm.Validate(info); err != nil {
				return err
			}

			fmt.Printf("using %s packager...\n", pkger)
			pkg, err := nfpm.Get(pkger)
			if err != nil {
				return err
			}

			releasePkg := pkg.ConventionalFileName(info)
			tgtPath := filepath.Join(appName+"-release", "linux-"+arch, releasePkg)
			f, err := os.Create(tgtPath)
			if err != nil {
				return err
			}

			sh := sha256.New()

			info.Target = tgtPath
			err = pkg.Package(info, io.MultiWriter(f, sh))
			_ = f.Close()
			if err != nil {
				os.Remove(tgtPath)
				return err
			}

			tgtShasum := sh.Sum(nil)
			tgtPathShasum := tgtPath + ".sha256sum"
			if err = ioutil.WriteFile(tgtPathShasum, []byte(fmt.Sprintf("%s  %s", hex.EncodeToString(tgtShasum), releasePkg)), 0644); err != nil {
				os.Remove(tgtPath)
				return err
			}
			fmt.Printf("created package: %s\n", tgtPath)
		}
	}
	var json = jsoniter.ConfigCompatibleWithStandardLibrary
	buf, err := json.Marshal(&d)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath.Join(appName+"-release", "downloads.json"), buf, 0644)
}
