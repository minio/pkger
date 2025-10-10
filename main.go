/*
 * Copyright (C) 2020-2025, MinIO, Inc.
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
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/alecthomas/kingpin"
	jsoniter "github.com/json-iterator/go"
	"gopkg.in/yaml.v2"

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

	noPackages = app.Flag("no-pkg", "do not build any packages").
			Default("false").
			Short('n').
			Bool()

	release = app.Flag("release", "Current release tag").
		Default("").
		Short('r').
		String()

	packager = app.Flag("packager", "Select packager implementation to use, defaults to: `deb,rpm,apk`").
			Default("deb,rpm,apk").
			Short('p').
			Enum("deb", "rpm", "apk", "deb,rpm,apk")

	license = app.Flag("license", "Set the license of this package, defaults to `AGPLv3`").
		Default("AGPLv3").Short('l').String()

	releaseDir = app.Flag("releaseDir", "Release directory (that contains os-arch specific dirs) to pick up binaries to package, defaults to `appName+\"-release\"`").
			Short('d').String()

	scriptsDir = app.Flag("scriptsDir", "Directory that contains package scripts (preinstall.sh, postinstall.sh, preremove.sh and postremove.sh), defaults to the current directory").
			Default("./").
			Short('s').String()

	deps = app.Flag("deps", "A json file that contains the dependencies for each package type").String()

	edge = app.Flag("edge", "Generate EDGE release URLs (uses /edge/ path instead of /release/)").
		Default("false").
		Short('e').
		Bool()
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
license: "{{ .License }}"
rpm:
  group: Applications/File
contents:
- src: {{ .ReleaseDir }}/{{ .OS }}-{{ .Arch }}/{{ .Binary }}.{{ .Release }}
  dst: /usr/local/bin/{{ .App }}
{{if or (eq .Binary "minio") (eq .Binary "aistor")}}
- src: minio.service
  dst: /lib/systemd/system/minio.service
{{end}}
{{if eq .Binary "sidekick"}}
- src: sidekick.service
  dst: /lib/systemd/system/sidekick.service
{{end}}
scripts:
{{- range $name, $path := .Scripts }}
  {{ $name }}: "{{ $path }}"
{{- end }}
overrides:
{{- range $pkg, $deps := .Deps }}
  {{ $pkg }}:
    depends:
{{range $deps}}
      {{print "- " .}}
{{end}}
{{- end }}
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
	HELM     *dlInfo `json:"HELM,omitempty"`
	Kubectl  *dlInfo `json:"kubectl,omitempty"`
	Podman   *dlInfo `json:"Podman,omitempty"`
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
	"amd64": "x86_64",
	"arm64": "aarch64",
}

var debArchMap = map[string]string{
	"amd64": "amd64",
	"arm64": "arm64",
}

func generateEnterpriseDownloadsJSON(semVerTag, appName, releaseTag string, isEdge bool) enterpriseDownloadsJSON {
	// Helper to determine path: "release" or "edge"
	pathSegment := "release"
	if isEdge {
		pathSegment = "edge"
	}

	d := enterpriseDownloadsJSON{
		Subscriptions: map[string]downloadsJSON{},
	}
	d.Subscriptions["Enterprise"] = downloadsJSON{
		Kubernetes: make(map[string]map[string]downloadJSON),
		Linux:      make(map[string]map[string]downloadJSON),
		Docker:     make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
		MacOS:      make(map[string]map[string]downloadJSON),
	}
	for subscription := range d.Subscriptions {
		if appName == "minio-enterprise" {
			// Linux
			d.Subscriptions[subscription].Linux["AIStor Server"] = map[string]downloadJSON{}
			d.Subscriptions[subscription].Linux["MinIO KMS"] = map[string]downloadJSON{}
			// Kubernetes
			d.Subscriptions[subscription].Kubernetes["AIStor Server"] = map[string]downloadJSON{}
			// Docker
			d.Subscriptions[subscription].Docker["AIStor Server"] = map[string]downloadJSON{}
			// Windows
			d.Subscriptions[subscription].Windows["AIStor Server"] = map[string]downloadJSON{}
			// MacOS
			d.Subscriptions[subscription].MacOS["AIStor Server"] = map[string]downloadJSON{}
		}
		if appName == "mc-enterprise" {
			// Linux
			d.Subscriptions[subscription].Linux["AIStor Client"] = map[string]downloadJSON{}
			// Kubernetes
			d.Subscriptions[subscription].Kubernetes["AIStor Client"] = map[string]downloadJSON{}
			// Docker
			d.Subscriptions[subscription].Docker["AIStor Client"] = map[string]downloadJSON{}
			// Windows
			d.Subscriptions[subscription].Windows["AIStor Client"] = map[string]downloadJSON{}
			// MacOS
			d.Subscriptions[subscription].MacOS["AIStor Client"] = map[string]downloadJSON{}
		}
	}

	for subscription := range d.Subscriptions {
		for _, arch := range []string{
			"amd64",
			"arm64",
		} {
			if appName == "mc-enterprise" {
				d.Subscriptions[subscription].Linux["AIStor Client"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/mc", pathSegment, arch),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/mc/%s/linux-%s/mc
chmod +x mc
./mc --version`, pathSegment, arch),

						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/mc.sha256sum", pathSegment, arch),
					},
					RPM: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/mcli-%s-1.%s.rpm", pathSegment, arch, semVerTag, rpmArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/mcli-%s-1.%s.rpm.sha256sum", pathSegment, arch, semVerTag, rpmArchMap[arch]),
						Text: fmt.Sprintf(`dnf install https://dl.min.io/aistor/mc/%s/linux-%s/mcli-%s-1.%s.rpm
mc --version`, pathSegment, arch, semVerTag, rpmArchMap[arch]),
					},
					Deb: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/mcli_%s_%s.deb", pathSegment, arch, semVerTag, debArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/mcli_%s_%s.deb.sha256sum", pathSegment, arch, semVerTag, debArchMap[arch]),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/mc/%s/linux-%s/mcli_%s_%s.deb
dpkg -i mcli_%s_%s.deb
mc --version`, pathSegment, arch, semVerTag, debArchMap[arch], semVerTag, debArchMap[arch]),
					},
				}

				d.Subscriptions[subscription].Docker["AIStor Client"][arch] = downloadJSON{
					Podman: &dlInfo{
						Text: fmt.Sprintf(`podman pull quay.io/minio/aistor/mc:%s
podman run --name my-mc --hostname my-mc -it --entrypoint /bin/bash --rm minio/mc
mc --version`, releaseTag),
					},
				}
			}
			if appName == "minio-enterprise" {
				d.Subscriptions[subscription].Kubernetes["AIStor Server"][arch] = downloadJSON{
					Text: ``,
				}

				d.Subscriptions[subscription].Linux["MinIO KMS"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minkms/%s/linux-%s/minkms", pathSegment, arch),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/minkms/%s/linux-%s/minkms
chmod +x minkms
./minkms --version`, pathSegment, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minkms/%s/linux-%s/minkms.sha256sum", pathSegment, arch),
					},
				}

				d.Subscriptions[subscription].Linux["AIStor Server"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/minio", pathSegment, arch),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/minio/%s/linux-%s/minio
chmod +x minio
./minio --version`, pathSegment, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/minio.sha256sum", pathSegment, arch),
					},
					RPM: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/minio-%s-1.%s.rpm", pathSegment, arch, semVerTag, rpmArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/minio-%s-1.%s.rpm.sha256sum", pathSegment, arch, semVerTag, rpmArchMap[arch]),
						Text: fmt.Sprintf(`dnf install https://dl.min.io/aistor/minio/%s/linux-%s/minio-%s-1.%s.rpm
minio --version`, pathSegment, arch, semVerTag, rpmArchMap[arch]),
					},
					Deb: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/minio_%s_%s.deb", pathSegment, arch, semVerTag, debArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/minio_%s_%s.deb.sha256sum", pathSegment, arch, semVerTag, debArchMap[arch]),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/minio/%s/linux-%s/minio_%s_%s.deb
dpkg -i minio_%s_%s.deb
minio --version`, pathSegment, arch, semVerTag, debArchMap[arch], semVerTag, debArchMap[arch]),
					},
				}

				d.Subscriptions[subscription].Docker["AIStor Server"][arch] = downloadJSON{
					Podman: &dlInfo{
						Text: fmt.Sprintf(`podman pull quay.io/minio/aistor/minio:%s
podman run minio/aistor/minio --version`, releaseTag),
					},
				}

			}
		}

		for _, arch := range []string{
			"arm64",
		} {
			if appName == "mc-enterprise" {
				d.Subscriptions[subscription].MacOS["AIStor Client"][arch] = downloadJSON{
					Homebrew: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/mc", pathSegment, arch),
						Text:     `brew install minio/aistor/mc`,
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/mc.sha256sum", pathSegment, arch),
					},
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/mc", pathSegment, arch),
						Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/aistor/mc/%s/darwin-%s/mc
chmod +x mc
./mc --version`, pathSegment, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/mc.sha256sum", pathSegment, arch),
					},
				}
			}
			if appName == "minio-enterprise" {
				d.Subscriptions[subscription].MacOS["AIStor Server"][arch] = downloadJSON{
					Homebrew: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/server/minio/%s/darwin-%s/minio", pathSegment, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/server/minio/%s/darwin-%s/minio.sha256sum", pathSegment, arch),
						Text:     `brew install minio/aistor/minio`,
					},

					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/darwin-%s/minio", pathSegment, arch),
						Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/aistor/minio/%s/darwin-%s/minio
chmod +x minio
./minio --version`, pathSegment, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/darwin-%s/minio.sha256sum", pathSegment, arch),
					},
				}

			}
		}

		for _, arch := range []string{
			"amd64",
		} {
			if appName == "mc-enterprise" {
				d.Subscriptions[subscription].Windows["AIStor Client"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/windows-%s/mc.exe", pathSegment, arch),
						Text: fmt.Sprintf(`Invoke-WebRequest -Uri "https://dl.min.io/aistor/mc/%s/windows-%s/mc.exe" -OutFile "mc.exe"
mc.exe --version`, pathSegment, arch),

						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/windows-%s/mc.exe.sha256sum", pathSegment, arch),
					},
				}
			}

			if appName == "minio-enterprise" {
				d.Subscriptions[subscription].Windows["AIStor Server"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/windows-%s/minio.exe", pathSegment, arch),
						Text: fmt.Sprintf(`Invoke-WebRequest -Uri "https://dl.min.io/aistor/minio/%s/windows-%s/minio.exe" -OutFile "minio.exe"
minio.exe --version`, pathSegment, arch),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/windows-%s/minio.exe.sha256sum", pathSegment, arch),
					},
				}

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
		"ppc64le",
	} {
		if appName == "minio" {
			d.Kubernetes["MinIO Server"][linuxArch] = downloadJSON{
				Kubectl: &dlInfo{
					Text: `kubectl apply -k github.com/minio/operator`,
				},
			}
			d.Docker["MinIO Server"][linuxArch] = downloadJSON{
				Podman: &dlInfo{
					Text: `podman pull quay.io/minio/minio:latest
podman run minio/minio --version`,
				},
			}
			d.Linux["MinIO Server"][linuxArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio", linuxArch),
					Text: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio
chmod +x minio
./minio --version`, linuxArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio.sha256sum", linuxArch),
				},
				RPM: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s-1.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio-%s-1.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Text: fmt.Sprintf(`dnf install https://dl.min.io/server/minio/release/linux-%s/minio-%s-1.%s.rpm
minio --version`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				},
				Deb: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
					Text: fmt.Sprintf(`wget https://dl.min.io/server/minio/release/linux-%s/minio_%s_%s.deb
dpkg -i minio_%s_%s.deb
minio --version`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
				},
			}
		}
		if appName == "mc" {
			d.Kubernetes["MinIO Client"][linuxArch] = downloadJSON{
				Kubectl: &dlInfo{
					Text: `kubectl run my-mc -i --tty --image minio/mc:latest --command -- bash
mc --version`,
				},
			}
			d.Docker["MinIO Client"][linuxArch] = downloadJSON{
				Podman: &dlInfo{
					Text: `podman pull quay.io/minio/mc:latest
podman run --name my-mc --hostname my-mc -it --entrypoint /bin/bash --rm minio/mc
mc --version`,
				},
			}
			d.Linux["MinIO Client"][linuxArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc", linuxArch),
					Text: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/linux-%s/mc
chmod +x mc
./mc --version`, linuxArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mc.sha256sum", linuxArch),
				},
				RPM: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli-%s-1.%s.rpm", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli-%s-1.%s.rpm.sha256sum", linuxArch, semVerTag, rpmArchMap[linuxArch]),
					Text: fmt.Sprintf(`dnf install https://dl.min.io/client/mc/release/linux-%s/mcli-%s-1.%s.rpm
mc --version`, linuxArch, semVerTag, rpmArchMap[linuxArch]),
				},
				Deb: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli_%s_%s.deb", linuxArch, semVerTag, debArchMap[linuxArch]),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/linux-%s/mcli_%s_%s.deb.sha256sum", linuxArch, semVerTag, debArchMap[linuxArch]),
					Text: fmt.Sprintf(`wget https://dl.min.io/client/mc/release/linux-%s/mcli_%s_%s.deb
dpkg -i mcli_%s_%s.deb
mc --version`, linuxArch, semVerTag, debArchMap[linuxArch], semVerTag, debArchMap[linuxArch]),
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
					Text:     `brew install minio/stable/minio`,
				},
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/darwin-%s/minio.sha256sum", macArch),
					Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/server/minio/release/darwin-%s/minio
chmod +x minio
./minio --version`, macArch),
				},
			}
		}
		if appName == "mc" {
			d.MacOS["MinIO Client"][macArch] = downloadJSON{
				Homebrew: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc.sha256sum", macArch),
					Text:     `brew install minio/stable/mc`,
				},
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc", macArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/darwin-%s/mc.sha256sum", macArch),
					Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/client/mc/release/darwin-%s/mc
chmod +x mc
./minio --version`, macArch),
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
					Text: fmt.Sprintf(`Invoke-WebRequest -Uri "https://dl.min.io/server/minio/release/windows-%s/minio.exe" -OutFile "minio.exe"
minio.exe --version`, winArch),
					Checksum: fmt.Sprintf("https://dl.min.io/server/minio/release/windows-%s/minio.exe.sha256sum", winArch),
				},
			}
		}
		if appName == "mc" {
			d.Windows["MinIO Client"][winArch] = downloadJSON{
				Bin: &dlInfo{
					Download: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe", winArch),
					Text: fmt.Sprintf(`Invoke-WebRequest -Uri "https://dl.min.io/client/mc/release/windows-%s/mc.exe" -OutFile "mc.exe"
mc.exe --version`, winArch),
					Checksum: fmt.Sprintf("https://dl.min.io/client/mc/release/windows-%s/mc.exe.sha256sum", winArch),
				},
			}
		}
	}
	return d
}

func generateSidekickDownloadsJSON(semVerTag, releaseTag string) downloadsJSON {
	d := downloadsJSON{
		Linux:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
		Kubernetes: make(map[string]map[string]downloadJSON),
		Docker:     make(map[string]map[string]downloadJSON),
	}

	d.Linux["MinIO Sidekick"] = map[string]downloadJSON{}
	d.Windows["MinIO Sidekick"] = map[string]downloadJSON{}
	d.Kubernetes["MinIO Sidekick"] = map[string]downloadJSON{}
	d.Docker["MinIO Sidekick"] = map[string]downloadJSON{}

	for _, arch := range []string{"amd64", "arm64"} {
		d.Kubernetes["MinIO Sidekick"][arch] = downloadJSON{
			Kubectl: &dlInfo{
				Text: fmt.Sprintf(`kubectl run my-sidekick -i --tty --image quay.io/minio/aistor/sidekick:%s --command -- bash
sidekick --version`, releaseTag),
			},
		}
		d.Docker["MinIO Sidekick"][arch] = downloadJSON{
			Podman: &dlInfo{
				Text: fmt.Sprintf(`podman pull quay.io/minio/aistor/sidekick:%s
podman run --name my-sidekick -it --rm quay.io/minio/aistor/sidekick:%s --version`, releaseTag, releaseTag),
			},
		}
		d.Linux["MinIO Sidekick"][arch] = downloadJSON{
			RPM: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick-%s-1.%s.rpm", arch, semVerTag, rpmArchMap[arch]),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick-%s-1.%s.rpm.sha256sum", arch, semVerTag, rpmArchMap[arch]),
				Text: fmt.Sprintf(`# Download the RPM package
wget https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick-%s-1.%s.rpm

# Install with yum/dnf
sudo yum install sidekick-%s-1.%s.rpm
# or
sudo dnf install sidekick-%s-1.%s.rpm`, arch, semVerTag, rpmArchMap[arch], semVerTag, rpmArchMap[arch], semVerTag, rpmArchMap[arch]),
			},
			Deb: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick_%s_%s.deb", arch, semVerTag, debArchMap[arch]),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick_%s_%s.deb.sha256sum", arch, semVerTag, debArchMap[arch]),
				Text: fmt.Sprintf(`# Download the DEB package
wget https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick_%s_%s.deb

# Install with apt
sudo apt install ./sidekick_%s_%s.deb
# or
sudo dpkg -i sidekick_%s_%s.deb`, arch, semVerTag, debArchMap[arch], semVerTag, debArchMap[arch], semVerTag, debArchMap[arch]),
			},
		}
	}

	// Windows: amd64 only
	d.Windows["MinIO Sidekick"]["amd64"] = downloadJSON{
		Bin: &dlInfo{
			Download: "https://dl.min.io/aistor/sidekick/release/windows-amd64/sidekick.exe",
			Text: `# Download from https://dl.min.io/aistor/sidekick/release/windows-amd64/sidekick.exe
# Add to PATH or run directly`,
			Checksum: "https://dl.min.io/aistor/sidekick/release/windows-amd64/sidekick.exe.sha256sum",
		},
	}

	return d
}

func generateWarpDownloadsJSON(version, releaseTag string) downloadsJSON {
	d := downloadsJSON{
		Linux:      make(map[string]map[string]downloadJSON),
		MacOS:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
		Kubernetes: make(map[string]map[string]downloadJSON),
		Docker:     make(map[string]map[string]downloadJSON),
	}

	d.Linux["MinIO Warp"] = map[string]downloadJSON{}
	d.MacOS["MinIO Warp"] = map[string]downloadJSON{}
	d.Windows["MinIO Warp"] = map[string]downloadJSON{}
	d.Kubernetes["MinIO Warp"] = map[string]downloadJSON{}
	d.Docker["MinIO Warp"] = map[string]downloadJSON{}

	// Linux: amd64 and arm64
	for _, arch := range []string{"amd64", "arm64"} {
		d.Kubernetes["MinIO Warp"][arch] = downloadJSON{
			Kubectl: &dlInfo{
				Text: fmt.Sprintf(`kubectl run my-warp -i --tty --image quay.io/minio/aistor/warp:%s --command -- bash
warp --version`, releaseTag),
			},
		}
		d.Docker["MinIO Warp"][arch] = downloadJSON{
			Podman: &dlInfo{
				Text: fmt.Sprintf(`podman pull quay.io/minio/aistor/warp:%s
podman run --name my-warp -it --rm quay.io/minio/aistor/warp:%s --version`, releaseTag, releaseTag),
			},
		}
		d.Linux["MinIO Warp"][arch] = downloadJSON{
			Bin: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/warp/release/linux-%s/warp", arch),
				Text: fmt.Sprintf(`wget https://dl.min.io/aistor/warp/release/linux-%s/warp -O warp
chmod +x warp
sudo mv warp /usr/local/bin/`, arch),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/warp/release/linux-%s/warp.sha256sum", arch),
			},
			RPM: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/warp/release/linux-%s/warp-%s-1.%s.rpm", arch, version, rpmArchMap[arch]),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/warp/release/linux-%s/warp-%s-1.%s.rpm.sha256sum", arch, version, rpmArchMap[arch]),
				Text: fmt.Sprintf(`wget https://dl.min.io/aistor/warp/release/linux-%s/warp-%s-1.%s.rpm
sudo rpm -ivh warp-%s-1.%s.rpm`, arch, version, rpmArchMap[arch], version, rpmArchMap[arch]),
			},
			Deb: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/warp/release/linux-%s/warp_%s_%s.deb", arch, version, debArchMap[arch]),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/warp/release/linux-%s/warp_%s_%s.deb.sha256sum", arch, version, debArchMap[arch]),
				Text: fmt.Sprintf(`wget https://dl.min.io/aistor/warp/release/linux-%s/warp_%s_%s.deb
sudo dpkg -i warp_%s_%s.deb`, arch, version, debArchMap[arch], version, debArchMap[arch]),
			},
		}
	}

	// macOS: arm64 only
	d.MacOS["MinIO Warp"]["arm64"] = downloadJSON{
		Bin: &dlInfo{
			Download: "https://dl.min.io/aistor/warp/release/darwin-arm64/warp",
			Text: `wget https://dl.min.io/aistor/warp/release/darwin-arm64/warp -O warp
chmod +x warp
sudo mv warp /usr/local/bin/`,
			Checksum: "https://dl.min.io/aistor/warp/release/darwin-arm64/warp.sha256sum",
		},
	}

	// Windows: amd64 only
	d.Windows["MinIO Warp"]["amd64"] = downloadJSON{
		Bin: &dlInfo{
			Download: "https://dl.min.io/aistor/warp/release/windows-amd64/warp.exe",
			Text: `# Download from https://dl.min.io/aistor/warp/release/windows-amd64/warp.exe
# Add to PATH or run directly`,
			Checksum: "https://dl.min.io/aistor/warp/release/windows-amd64/warp.exe.sha256sum",
		},
	}

	return d
}

func releaseDirName() string {
	if *releaseDir != "" {
		return *releaseDir
	}
	name := *appName
	switch name {
	case "minio-enterprise":
		name = "minio"
	case "mc-enterprise":
		name = "mc"
	}
	return name + "-release"
}

func main() {
	app.Version(version)
	app.VersionFlag.Short('v')
	app.HelpFlag.Short('h')
	if _, err := app.Parse(os.Args[1:]); err != nil {
		kingpin.Fatalf(err.Error())
	}

	// Skip package building for warp (uses goreleaser) - only generate JSON
	if !*noPackages && *appName != "warp" {
		if err := doPackage(*appName, *license, *release, *packager, *deps, *scriptsDir); err != nil {
			if !*ignoreMissingArch {
				kingpin.Fatalf(err.Error())
			} else {
				kingpin.Errorf(err.Error())
			}
		}
	}

	var d any
	json := jsoniter.ConfigCompatibleWithStandardLibrary

	// Determine output filename based on edge flag
	outputFilename := "downloads-" + *appName
	if *edge {
		outputFilename += "-edge"
	}
	outputFilename += ".json"

	switch *appName {
	case "minio-enterprise", "mc-enterprise":
		semVerTag := semVerRelease(*release)
		d = generateEnterpriseDownloadsJSON(semVerTag, *appName, *release, *edge)
	case "sidekick":
		semVerTag := semVerRelease(*release)
		d = generateSidekickDownloadsJSON(semVerTag, *release)
	case "warp":
		// Warp uses semantic versioning (e.g., v0.4.3), not date-based releases
		// Validate format: vX.Y.Z where X, Y, Z are numbers
		if !strings.HasPrefix(*release, "v") {
			kingpin.Fatalf("warp release version must start with 'v' (e.g., v0.4.3), got: %s", *release)
		}
		versionWithoutV := strings.TrimPrefix(*release, "v")
		// Validate semantic version format X.Y.Z
		semverPattern := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
		if !semverPattern.MatchString(versionWithoutV) {
			kingpin.Fatalf("warp release version must follow semantic versioning vX.Y.Z (e.g., v0.4.3), got: %s", *release)
		}
		// Strip 'v' prefix for package naming conventions
		d = generateWarpDownloadsJSON(versionWithoutV, *release)
	default:
		semVerTag := semVerRelease(*release)
		d = generateDownloadsJSON(semVerTag, *appName)
	}

	buf, err := json.Marshal(&d)
	if err != nil {
		kingpin.Fatalf(err.Error())
	}

	outputPath := filepath.Join(releaseDirName(), outputFilename)
	os.WriteFile(outputPath, buf, 0o644)

	fmt.Println("Generated downloads metadata at", outputPath)
}

type releaseTmpl struct {
	App           string
	License       string
	ReleaseDir    string
	Binary        string
	Description   string
	OS            string
	Arch          string
	Release       string
	SemVerRelease string

	Scripts map[string]string
	Deps    map[string][]string
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

func parseDepsFile(path string) (map[string][]string, error) {
	depsBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := make(map[string][]string)
	err = yaml.Unmarshal(depsBytes, &d)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// nolint:funlen
func doPackage(appName, license, release, packager, deps, scriptsDir string) error {
	var pkgDeps map[string][]string
	if deps != "" {
		var err error
		pkgDeps, err = parseDepsFile(deps)
		if err != nil {
			return err
		}
	}

	mtmpl, err := template.New("minio").Parse(tmpl)
	if err != nil {
		return err
	}

	// Warp uses semantic versioning (vX.Y.Z), not date-based releases
	// Strip 'v' prefix for package version field
	var semVerTag string
	if appName == "warp" {
		semVerTag = strings.TrimPrefix(release, "v")
	} else {
		semVerTag = semVerRelease(release)
	}

	for _, arch := range []string{
		"amd64",
		"arm64",
		"ppc64le",
	} {
		if appName == "minio-enterprise" && arch != "amd64" && arch != "arm64" {
			continue
		}
		if appName == "mc-enterprise" && arch != "amd64" && arch != "arm64" {
			continue
		}
		if appName == "sidekick" && arch != "amd64" && arch != "arm64" {
			continue
		}
		if appName == "warp" && arch != "amd64" && arch != "arm64" {
			continue
		}

		var buf bytes.Buffer
		err = mtmpl.Execute(&buf, releaseTmpl{
			App: func() string {
				if appName == "minio-enterprise" {
					return "minio"
				}
				if appName == "mc" || appName == "mc-enterprise" {
					return "mcli"
				}
				return appName
			}(),
			License: func() string {
				return license
			}(),
			ReleaseDir: releaseDirName(),
			Binary: func() string {
				if appName == "minio-enterprise" {
					return "minio"
				}
				if appName == "mc-enterprise" {
					return "mc"
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
				if appName == "mc" || appName == "mc-enterprise" {
					return `MinIO Client for cloud storage and filesystems`
				}
				return `MinIO is a High Performance Object Storage released under AGPLv3.
  It is API compatible with Amazon S3 cloud storage service. Use MinIO to build
  high performance infrastructure for machine learning, analytics and application
  data workloads.`
			}(),
			Scripts: func() (scripts map[string]string) {
				scripts = make(map[string]string)
				for _, s := range []string{"preinstall", "postinstall", "preremove", "postremove"} {
					path := filepath.Join(scriptsDir, s+".sh")
					if _, err := os.Stat(path); err == nil {
						scripts[s] = path
					} else if !os.IsNotExist(err) {
						fmt.Printf("unable to access to %s: %s \n", path, err)
					}
				}
				return
			}(),
			Deps:          pkgDeps,
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
			tgtPath := filepath.Join(releaseDirName(), "linux-"+arch, releasePkg)
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
						return "minio"
					}
					return appName
				}() + filepath.Ext(tgtPath))
				_ = os.Symlink(releasePkg, func() string {
					if appName == "minio-enterprise" {
						return "minio"
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

	return nil
}
