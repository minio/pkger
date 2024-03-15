/*
 * Copyright (C) 2020-2024, MinIO, Inc.
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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/alecthomas/kingpin"
	jsoniter "github.com/json-iterator/go"

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
	ignoreMissingArch = app.Flag("ignore", "ignore any missing arch while packaging").
				Default("false").
				Short('i').
				Bool()
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
  {{ .Description }}
vendor: "MinIO, Inc."
homepage: "https://min.io"
license: "AGPLv3"
rpm:
  group: Applications/File
contents:
- src: {{ .ReleaseDir }}-release/{{ .OS }}-{{ .Arch }}/{{ .Binary }}.{{ .Release }}
  dst: /usr/local/bin/{{ .App }}
{{if eq .Binary "minio" }}
- src: minio.service
  dst: /lib/systemd/system/minio.service
{{end}}
{{if eq .Binary "mineos" }}
- src: minio.service
  dst: /lib/systemd/system/minio.service
{{end}}
`

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

type enterpriseDownloadsJSON struct {
	Subscriptions map[string]downloadsJSON
}

type downloadsJSON struct {
	Kubernetes map[string]map[string]downloadJSON `json:"Kubernetes"`
	Docker     map[string]map[string]downloadJSON `json:"Docker,omitempty"`
	Linux      map[string]map[string]downloadJSON `json:"Linux"`
	MacOS      map[string]map[string]downloadJSON `json:"macOS,omitempty"`
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

func generateEnterpriseDownloadsJSON(semVerTag string) enterpriseDownloadsJSON {
	d := enterpriseDownloadsJSON{
		Subscriptions: map[string]downloadsJSON{},
	}
	d.Subscriptions["Standard"] = downloadsJSON{
		Kubernetes: make(map[string]map[string]downloadJSON),
		Linux:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
	}
	d.Subscriptions["Enterprise"] = downloadsJSON{
		Kubernetes: make(map[string]map[string]downloadJSON),
		Linux:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
	}
	d.Subscriptions["Enterprise-Lite"] = downloadsJSON{
		Kubernetes: make(map[string]map[string]downloadJSON),
		Linux:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
	}
	d.Subscriptions["Enterprise-Plus"] = downloadsJSON{
		Kubernetes: make(map[string]map[string]downloadJSON),
		Linux:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
	}
	for subscription := range d.Subscriptions {
		d.Subscriptions[subscription].Linux["MinIO Object Store"] = map[string]downloadJSON{}
		d.Subscriptions[subscription].Windows["MinIO Object Store"] = map[string]downloadJSON{}
		if subscription == "Enterprise-Lite" || subscription == "Enterprise-Plus" {
			d.Subscriptions[subscription].Linux["MinIO KMS"] = map[string]downloadJSON{}
			d.Subscriptions[subscription].Linux["MinIO Catalog"] = map[string]downloadJSON{}
			d.Subscriptions[subscription].Linux["MinIO Firewall"] = map[string]downloadJSON{}
			d.Subscriptions[subscription].Linux["MinIO Cache"] = map[string]downloadJSON{}
			d.Subscriptions[subscription].Kubernetes["MinIO Enterprise Object Store"] = map[string]downloadJSON{}
		} else {
			d.Subscriptions[subscription].Kubernetes["MinIO Object Store"] = map[string]downloadJSON{}
		}
	}

	for subscription := range d.Subscriptions {
		for _, arch := range []string{
			"amd64",
			"arm64",
		} {
			if arch == "amd64" {
				d.Subscriptions[subscription].Windows["MinIO Object Store"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/windows-%s/minio.exe", arch),
						Text: fmt.Sprintf(`PS> Invoke-WebRequest -Uri "https://dl.min.io/enterprise/minio/release/windows-%s/minio.exe" -OutFile "C:\minio.exe"
PS> setx MINIO_ROOT_USER admin
PS> setx MINIO_ROOT_PASSWORD password
PS> C:\minio.exe server F:\Data --console-address ":9001"`, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/windows-%s/minio.exe.sha256sum", arch),
					},
				}
			}
			if subscription == "Standard" || subscription == "Enterprise" {
				d.Subscriptions[subscription].Kubernetes["MinIO Object Store"][arch] = downloadJSON{
					Text: `wget https://dl.min.io/enterprise/operator.tar.gz
tar xvf operator.tar.gz
kubectl apply -k operator`,
				}
			} else {
				d.Subscriptions[subscription].Kubernetes["MinIO Enterprise Object Store"][arch] = downloadJSON{
					Text: `wget https://dl.min.io/enterprise/console.tar.gz
tar xvf console.tar.gz
kubectl apply -k console`,
				}
				d.Subscriptions[subscription].Linux["MinIO Cache"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/enterprise/mincache/release/linux-%s/mincache", arch),
						Text: fmt.Sprintf(`wget https://dl.min.io/enterprise/mincache/release/linux-%s/mincache
chmod +x mincache
./mincache serve --config config.yaml`, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/enterprise/mincache/release/linux-%s/mincache.sha256sum", arch),
					},
				}
				d.Subscriptions[subscription].Linux["MinIO Firewall"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/enterprise/minwall/release/linux-%s/minwall", arch),
						Text: fmt.Sprintf(`wget https://dl.min.io/enterprise/minwall/release/linux-%s/minwall
chmod +x minwall
./minwall -c config.yaml`, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/enterprise/minwall/release/linux-%s/minwall.sha256sum", arch),
					},
				}
				d.Subscriptions[subscription].Linux["MinIO KMS"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/enterprise/minkms/release/linux-%s/minkms", arch),
						Text: fmt.Sprintf(`wget https://dl.min.io/enterprise/minkms/release/linux-%s/minkms
chmod +x minkms
./minkms --help`, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/enterprise/minkms/release/linux-%s/minkms.sha256sum", arch),
					},
				}
				d.Subscriptions[subscription].Linux["MinIO Catalog"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/enterprise/mincat/release/linux-%s/mincat", arch),
						Text: fmt.Sprintf(`wget https://dl.min.io/enterprise/mincat/release/linux-%s/mincat
chmod +x mincat
./mincat --help`, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/enterprise/mincat/release/linux-%s/mincat.sha256sum", arch),
					},
				}
			}
			d.Subscriptions[subscription].Linux["MinIO Object Store"][arch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/linux-%s/minio", arch),
					Text: fmt.Sprintf(`wget https://dl.min.io/enterprise/minio/release/linux-%s/minio
chmod +x minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password ./minio server /mnt/data --console-address ":9001"`, arch),
					Checksum: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/linux-%s/minio.sha256sum", arch),
				},
				RPM: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/linux-%s/minio-%s-1.%s.rpm", arch, semVerTag, rpmArchMap[arch]),
					Checksum: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/linux-%s/minio-%s-1.%s.rpm.sha256sum", arch, semVerTag, rpmArchMap[arch]),
					Text: fmt.Sprintf(`dnf install https://dl.min.io/enterprise/minio/release/linux-%s/minio-%s-1.%s.rpm
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data --console-address ":9001"`, arch, semVerTag, rpmArchMap[arch]),
				},
				Deb: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/linux-%s/minio_%s_%s.deb", arch, semVerTag, debArchMap[arch]),
					Checksum: fmt.Sprintf("https://dl.min.io/enterprise/minio/release/linux-%s/minio_%s_%s.deb.sha256sum", arch, semVerTag, debArchMap[arch]),
					Text: fmt.Sprintf(`wget https://dl.min.io/enterprise/minio/release/linux-%s/minio_%s_%s.deb
dpkg -i minio_%s_%s.deb
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data --console-address ":9001"`, arch, semVerTag, debArchMap[arch], semVerTag, debArchMap[arch]),
				},
			}
		}
	}
	return d
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
				Text: `kubectl apply -k github.com/minio/operator`,
			}
			d.Docker["MinIO Server"][linuxArch] = downloadJSON{
				Text: `podman run -p 9000:9000 -p 9001:9001 minio/minio server /data --console-address ":9001"`,
			}
			d.Linux["MinIO Server"][linuxArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio", linuxArch),
					Text: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio
chmod +x minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password ./minio server /mnt/data --console-address ":9001"`, linuxArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio.sha256sum", linuxArch),
				},
				RPM: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s-1.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s-1.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Text: fmt.Sprintf(`dnf install https://dl.min.io/server/minio/release/linux-%s/minio-%s-1.%s.rpm
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data --console-address ":9001"`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				},
				Deb: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
					Text: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb
dpkg -i minio_%s_%s.deb
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data --console-address ":9001"`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
				},
			}
		}
		if appName == "mc" {
			d.Kubernetes["MinIO Client"][linuxArch] = downloadJSON{
				Text: `kubectl run my-mc -i --tty --image minio/mc:latest --command -- bash
[root@my-mc /]# mc alias set myminio/ https://minio.default.svc.cluster.local MY-USER MY-PASSWORD
[root@my-mc /]# mc ls myminio/mybucket`,
			}
			d.Docker["MinIO Client"][linuxArch] = downloadJSON{
				Text: `podman run --name my-mc --hostname my-mc -it --entrypoint /bin/bash --rm minio/mc
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
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli-%s-1.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli-%s-1.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Text: fmt.Sprintf(`dnf install https://dl.min.io/client/mc/release/linux-%s/mcli-%s-1.%s.rpm
mcli alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				},
				Deb: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
					Text: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/linux-%s/mcli_%s_%s.deb
dpkg -i mcli_%s_%s.deb
mcli alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
				},
			}
		}
	}

	for _, macArch := range []string{
		"amd64",
		"arm64",
	} {
		if appName == "minio" {
			d.MacOS["MinIO Server"][macArch] = downloadJSON{
				Homebrew: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio.sha256sum", macArch),
					Text: `brew install minio/stable/minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password minio server /mnt/data --console-address ":9001"`,
				},
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio.sha256sum", macArch),
					Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/server/minio/release/darwin-%s/minio
chmod +x minio
MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password ./minio server /mnt/data --console-address ":9001"`, macArch),
				},
			}
		}
		if appName == "mc" {
			d.MacOS["MinIO Client"][macArch] = downloadJSON{
				Homebrew: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc.sha256sum", macArch),
					Text: `brew install minio/stable/mc
mc alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`,
				},
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc.sha256sum", macArch),
					Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/client/mc/release/darwin-%s/mc
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
PS> C:\minio.exe server F:\Data --console-address ":9001"`, winArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/windows-%s/minio.exe.sha256sum", winArch),
				},
			}
		}
		if appName == "mc" {
			d.Windows["MinIO Client"][winArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe", winArch),
					Text: fmt.Sprintf(`PS> Invoke-WebRequest -Uri "https://dl.minio.io/client/mc/release/windows-%s/mc.exe" -OutFile "C:\mc.exe"
C:\mc.exe alias set myminio/ http://MINIO-SERVER MYUSER MYPASSWORD`, winArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe.sha256sum", winArch),
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
	if _, err := app.Parse(os.Args[1:]); err != nil {
		kingpin.Fatalf(err.Error())
	}

	if err := doPackage(*appName, *release, *packager); err != nil {
		kingpin.Fatalf(err.Error())
	}
}

type releaseTmpl struct {
	App           string
	ReleaseDir    string
	Binary        string
	Description   string
	OS            string
	Arch          string
	Release       string
	SemVerRelease string
}

const (
	minioReleaseTagTimeLayout    = "2006-01-02T15-04-05Z"
	minioPkgReleaseTagTimeLayout = "20060102150405"
)

// releaseTagToReleaseTime - reverse of `releaseTimeToReleaseTag()`
func releaseTagToReleaseTime(releaseTag string) (releaseTime time.Time, fields []string, err error) {
	fields = strings.Split(releaseTag, ".")
	if len(fields) < 2 || len(fields) > 4 {
		return releaseTime, nil, fmt.Errorf("%s is not a valid release tag", releaseTag)
	}
	if fields[0] != "RELEASE" {
		return releaseTime, nil, fmt.Errorf("%s is not a valid release tag", releaseTag)
	}
	releaseTime, err = time.Parse(minioReleaseTagTimeLayout, fields[1])
	return releaseTime, fields, err
}

func semVerRelease(release string) string {
	rtime, fields, err := releaseTagToReleaseTime(release)
	if err != nil {
		panic(err)
	}
	var hotfixStr string
	if len(fields) == 4 {
		hotfixStr = fields[2] + "." + fields[3]
	}
	if hotfixStr != "" {
		return rtime.Format(minioPkgReleaseTagTimeLayout) + ".0.0." + hotfixStr
	}
	return rtime.Format(minioPkgReleaseTagTimeLayout) + ".0.0"
}

// nolint:funlen
func doPackage(appName, release, packager string) error {
	mtmpl, err := template.New("minio").Parse(tmpl)
	if err != nil {
		return err
	}

	semVerTag := semVerRelease(release)
	for _, arch := range []string{
		"amd64",
		"arm64",
		"s390x",
		"ppc64le",
	} {
		if appName == "minio-enterprise" && arch != "amd64" && arch != "arm64" {
			continue
		}

		var buf bytes.Buffer
		err = mtmpl.Execute(&buf, releaseTmpl{
			App: func() string {
				if appName == "minio-enterprise" {
					return "minio"
				}
				if appName == "mc" {
					return "mcli"
				}
				return appName
			}(),
			ReleaseDir: func() string {
				if appName == "minio-enterprise" {
					return "mineos"
				}
				return appName
			}(),
			Binary: func() string {
				if appName == "minio-enterprise" {
					return "minio"
				}
				return appName
			}(),
			Description: func() string {
				if appName == "minio-enterprise" {
					return `MinIO is a High Performance Object Store.
  It is API compatible with Amazon S3 cloud storage service. Use MinIO to build
  high performance infrastructure for machine learning, analytics and application
  data workloads.`
				}
				if appName == "mc" {
					return `MinIO Client for cloud storage and filesystems`
				}
				return `MinIO is a High Performance Object Storage released under AGPLv3.
  It is API compatible with Amazon S3 cloud storage service. Use MinIO to build
  high performance infrastructure for machine learning, analytics and application
  data workloads.`
			}(),
			OS:            "linux",
			Arch:          arch,
			Release:       release,
			SemVerRelease: semVerTag,
		})
		if err != nil {
			return err
		}

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
				if *ignoreMissingArch {
					continue
				}
				return err
			}

			fmt.Printf("using %s packager...\n", pkger)
			pkg, err := nfpm.Get(pkger)
			if err != nil {
				return err
			}

			releasePkg := pkg.ConventionalFileName(info)
			tgtPath := filepath.Join(func() string {
				if appName == "minio-enterprise" {
					return "mineos"
				}
				return appName
			}()+"-release", "linux-"+arch, releasePkg)
			f, err := os.Create(tgtPath)
			if err != nil {
				return err
			}

			{
				curDir, err := os.Getwd()
				if err != nil {
					return err
				}

				_ = os.Chdir(filepath.Dir(tgtPath))
				_ = os.Remove(func() string {
					if appName == "minio-enterprise" {
						return "mineos"
					}
					return appName
				}() + filepath.Ext(tgtPath))
				_ = os.Symlink(releasePkg, func() string {
					if appName == "minio-enterprise" {
						return "mineos"
					}
					return appName
				}()+filepath.Ext(tgtPath))
				_ = os.Chdir(curDir)
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
			if err = os.WriteFile(tgtPathShasum, []byte(fmt.Sprintf("%s  %s", hex.EncodeToString(tgtShasum), releasePkg)), 0o644); err != nil {
				os.Remove(tgtPath)
				return err
			}
			fmt.Printf("created package: %s\n", tgtPath)
		}
	}

	var d any
	json := jsoniter.ConfigCompatibleWithStandardLibrary
	if appName == "minio-enterprise" {
		d = generateEnterpriseDownloadsJSON(semVerTag)
	} else {
		d = generateDownloadsJSON(semVerTag, appName)
	}

	buf, err := json.Marshal(&d)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(func() string {
		if appName == "minio-enterprise" {
			return "mineos"
		}
		return appName
	}()+"-release", "downloads-"+appName+".json"), buf, 0o644)
}
