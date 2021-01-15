// +build go1.14

/*
 * Copyright (C) 2020, MinIO, Inc.
 *
 * This code is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, version 3,
 * as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License, version 3,
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 *
 */

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

type dlInfo struct {
	Text     string `json:"text"`
	Checksum string `json:"cksum"`
	Download string `json:"download"`
}

type downloadJSON struct {
	Text     string  `json:"text,omitempty"`
	Bin      *dlInfo `json:"Binary,omitempty"`
	RPM      *dlInfo `json:"RPM,omitempty"`
	Deb      *dlInfo `json:"DEB,omitempty"`
	Homebrew *dlInfo `json:"Homebrew,omitempty"`
}

type downloadsJSON struct {
	Kubernetes map[string]map[string]downloadJSON `json:"Kubernetes"`
	Docker     map[string]map[string]downloadJSON `json:"Docker"`
	Linux      map[string]map[string]downloadJSON `json:"Linux"`
	MacOS      map[string]map[string]downloadJSON `json:"MacOS"`
	Windows    map[string]map[string]downloadJSON `json:"Windows"`
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

func generateDownloadsJSON(semVerTag string, appName string) downloadsJSON {
	d := downloadsJSON{
		Linux:      make(map[string]map[string]downloadJSON),
		MacOS:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
		Docker:     make(map[string]map[string]downloadJSON),
		Kubernetes: make(map[string]map[string]downloadJSON),
	}

	if appName == "minio" {
		d.Linux["MinIO Server"] = map[string]downloadJSON{}
		d.MacOS["MinIO Server"] = map[string]downloadJSON{}
		d.Windows["MinIO Server"] = map[string]downloadJSON{}
		d.Docker["MinIO Server"] = map[string]downloadJSON{}
		d.Kubernetes["MinIO Server"] = map[string]downloadJSON{}
	}
	if appName == "mc" {
		d.Linux["MinIO Client"] = map[string]downloadJSON{}
		d.MacOS["MinIO Client"] = map[string]downloadJSON{}
		d.Windows["MinIO Client"] = map[string]downloadJSON{}
		d.Docker["MinIO Client"] = map[string]downloadJSON{}
		d.Kubernetes["MinIO Client"] = map[string]downloadJSON{}
	}
	for _, linuxArch := range []string{
		"amd64",
		"arm64",
		"s390x",
		"ppc64le",
	} {
		if appName == "minio" {
			d.Kubernetes["MinIO Server"][linuxArch] = downloadJSON{
				Text: `kubectl krew install minio
kubectl minio init
kubectl minio tenant create --name tenant1 --servers 4 --volumes 16 --capacity 16Ti`,
			}
			d.Docker["MinIO Server"][linuxArch] = downloadJSON{
				Text: `docker run -p 9000:9000 minio/minio server /data`,
			}
			d.Linux["MinIO Server"][linuxArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio", linuxArch),
					Text: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio
chmod +x minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password ./minio server /mnt/data`, linuxArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio.sha256sum", linuxArch),
				},
				RPM: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Text: fmt.Sprintf(`dnf install https://dl.min.io/server/minio/release/linux-%s/minio-%s.%s.rpm
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				},
				Deb: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
					Text: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb
dpkg -i minio_%s_%s.deb
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
				},
			}
		}
		if appName == "mc" {
			d.Kubernetes["MinIO Client"][linuxArch] = downloadJSON{
				Text: `kubectl run my-mc -i --tty --image minio/mc:latest --command -- bash
[root@my-mc /]# mc alias set myminio/ https://minio.svc.cluster.local MY-USER MY-PASSWORD
[root@my-mc /]# mc ls myminio/mybucket`,
			}
			d.Docker["MinIO Client"][linuxArch] = downloadJSON{
				Text: `docker run --name my-mc --hostname my-mc -it --entrypoint /bin/bash --rm minio/mc
[root@my-mc /]# mc alias set myminio/ https://my-minio-service MY-USER MY-PASSWORD
[root@my-mc /]# mc ls myminio/mybucket`,
			}
			d.Linux["MinIO Client"][linuxArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc", linuxArch),
					Text: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/linux-%s/mc
chmod +x mc
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc.sha256sum", linuxArch),
				},
				RPM: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc-%s.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc-%s.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Text: fmt.Sprintf(`dnf install https://dl.min.io/client/mc/release/linux-%s/mc-%s.%s.rpm
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				},
				Deb: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
					Text: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/linux-%s/mc_%s_%s.deb
dpkg -i minio_%s_%s.deb
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
				},
			}
		}
	}
	for _, macArch := range []string{
		"amd64",
	} {
		if appName == "minio" {
			d.MacOS["MinIO Server"][macArch] = downloadJSON{
				Homebrew: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio.sha56sum", macArch),
					Text: `brew install minio/stable/minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data`,
				},
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio.sha56sum", macArch),
					Text: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/darwin-%s/minio
chmod +x minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password ./minio server /mnt/data`, macArch),
				},
			}
		}
		if appName == "mc" {
			d.MacOS["MinIO Client"][macArch] = downloadJSON{
				Homebrew: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc.sha56sum", macArch),
					Text: `brew install minio/stable/mc
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`,
				},
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc.sha56sum", macArch),
					Text: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/darwin-%s/mc
chmod +x mc
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, macArch),
				},
			}
		}
	}
	for _, winArch := range []string{
		"amd64",
	} {
		if appName == "minio" {
			d.Windows["MinIO Server"][winArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/windows-%s/minio.exe", winArch),
					Text: fmt.Sprintf(`PS> Invoke-WebRequest -Uri "https://dl.min.io/server/minio/release/windows-%s/minio.exe" -OutFile "C:\minio.exe"
PS> setx MINIO_ROOT_USER admin
PS> setx MINIO_ROOT_PASSWORD password
PS> C:\minio.exe server F:\Data`, winArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/windows-%s/minio.exe.sha56sum", winArch),
				},
			}
		}
		if appName == "mc" {
			d.Windows["MinIO Client"][winArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe", winArch),
					Text: fmt.Sprintf(`PS> Invoke-WebRequest -Uri "https://dl.minio.io/client/mc/release/windows-amd64/mc.exe" -OutFile "C:\mc.exe"
C:\mc.exe alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, winArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe.sha56sum", winArch),
				},
			}
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
	d := generateDownloadsJSON(semVerTag, appName)
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
	return ioutil.WriteFile(filepath.Join(appName+"-release", "downloads-"+appName+".json"), buf, 0644)
}
