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
	"runtime/debug"
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
	version        = "dev"
	commitID       = ""
	dirty          = false
	releaseMatcher = regexp.MustCompile(`[0-9]`)

	app     = kingpin.New("pkger", "Debian, RPMs and APKs for MinIO")
	appName = app.Flag("appName", "Application name for the package: aistor, ac, sidekick, warp, memkv, aimem or minfs").
		Default("aistor").
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

	noJSON = app.Flag("no-json", "Skip JSON metadata file generation").
		Default("false").
		Short('j').
		Bool()

	contentsFile = app.Flag("contents", "YAML file with additional nfpm content entries (src/dst/type), supports ${ARCH} expansion").
			Short('c').
			String()

	binaryName = app.Flag("binary-name", "Override the source binary base name read from the release dir (<releaseDir>/<os>-<arch>/<binary-name>.<release>) and the raw-binary filename in the downloads metadata. Defaults to the per-app convention.").
			String()

	packageName = app.Flag("package-name", "Override the package name and the installed command under /usr/local/bin. When it differs from the app's default package name, a back-compat symlink at the old name is added and the package declares provides/replaces/conflicts on the old name (e.g. --package-name aistor => /usr/local/bin/aistor + /usr/local/bin/minio -> aistor, replaces minio). Defaults to the per-app convention.").
			String()
)

type extraContent struct {
	Src  string `yaml:"src"`
	Dst  string `yaml:"dst"`
	Type string `yaml:"type,omitempty"`
}

func parseContentsFile(path string) ([]extraContent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var contents []extraContent
	if err := yaml.Unmarshal(data, &contents); err != nil {
		return nil, err
	}
	return contents, nil
}

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		// Get version from module info
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		// Get VCS info (commit, dirty status)
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				commitID = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			}
		}
	}
}

func getVersionString() string {
	v := version
	// Only add commit ID if not already in version string (pseudo-versions include it)
	if commitID != "" && !strings.Contains(v, commitID[:12]) {
		// Use short commit ID (first 12 chars)
		short := commitID
		if len(short) > 12 {
			short = short[:12]
		}
		v += " (" + short + ")"
	}
	// Only add dirty if not already in version string
	if dirty && !strings.Contains(v, "dirty") {
		v += " dirty"
	}
	return v
}

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
{{if .LegacyName}}
provides:
- {{ .LegacyName }}
replaces:
- {{ .LegacyName }}
conflicts:
- {{ .LegacyName }}
{{end}}
rpm:
  group: Applications/File
contents:
- src: {{ .ReleaseDir }}/{{ .OS }}-{{ .Arch }}/{{ .Binary }}.{{ .Release }}
  dst: /usr/local/bin/{{ .App }}
{{if .LegacyName}}
- src: {{ .App }}
  dst: /usr/local/bin/{{ .LegacyName }}
  type: symlink
{{end}}
{{if or (eq .Binary "minio") (eq .Binary "aistor")}}
- src: minio.service
  dst: /lib/systemd/system/minio.service
{{end}}
{{if eq .Binary "sidekick"}}
- src: sidekick.service
  dst: /lib/systemd/system/sidekick.service
{{end}}
{{- range .ExtraContents }}
- src: {{ .Src }}
  dst: {{ .Dst }}
{{- if .Type }}
  type: {{ .Type }}
{{- end }}
{{- end }}
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

// pkgArches is every architecture pkger packages for. Every supported app
// (aistor, ac, sidekick, warp, memkv, aimem, minfs) builds for exactly these
// two; ppc64le went away with the community minio/mc packages.
var pkgArches = []string{"amd64", "arm64"}

// generateEnterpriseDownloadsJSON builds the downloads metadata. binFile is the
// raw downloadable binary filename (e.g. "aistor", "ac") and pkgFile is the
// package filename base / rpm-deb name (e.g. "aistor", "acli"). dl paths are
// unchanged by the rename; only these filenames change.
func generateEnterpriseDownloadsJSON(semVerTag, appName, releaseTag, binFile, pkgFile string, isEdge bool) enterpriseDownloadsJSON {
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
		if appName == "aistor" {
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
		if appName == "ac" {
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
			if appName == "ac" {
				d.Subscriptions[subscription].Linux["AIStor Client"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/%s", pathSegment, arch, binFile),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/mc/%s/linux-%s/%s
chmod +x %s
./%s --version`, pathSegment, arch, binFile, binFile, binFile),

						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/%s.sha256sum", pathSegment, arch, binFile),
					},
					RPM: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/%s-%s-1.%s.rpm", pathSegment, arch, pkgFile, semVerTag, rpmArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/%s-%s-1.%s.rpm.sha256sum", pathSegment, arch, pkgFile, semVerTag, rpmArchMap[arch]),
						Text: fmt.Sprintf(`dnf install https://dl.min.io/aistor/mc/%s/linux-%s/%s-%s-1.%s.rpm
%s --version`, pathSegment, arch, pkgFile, semVerTag, rpmArchMap[arch], pkgFile),
					},
					Deb: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/%s_%s_%s.deb", pathSegment, arch, pkgFile, semVerTag, debArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/linux-%s/%s_%s_%s.deb.sha256sum", pathSegment, arch, pkgFile, semVerTag, debArchMap[arch]),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/mc/%s/linux-%s/%s_%s_%s.deb
dpkg -i %s_%s_%s.deb
%s --version`, pathSegment, arch, pkgFile, semVerTag, debArchMap[arch], pkgFile, semVerTag, debArchMap[arch], pkgFile),
					},
				}

				d.Subscriptions[subscription].Docker["AIStor Client"][arch] = downloadJSON{
					Podman: &dlInfo{
						Text: fmt.Sprintf(`podman pull quay.io/minio/aistor/mc:%s
podman run --name my-mc --hostname my-mc -it --entrypoint /bin/bash --rm minio/mc
quay.io/minio/aistor/mc
mc --version`, releaseTag),
					},
				}
			}
			if appName == "aistor" {
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
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/%s", pathSegment, arch, binFile),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/minio/%s/linux-%s/%s
chmod +x %s
./%s --version`, pathSegment, arch, binFile, binFile, binFile),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/%s.sha256sum", pathSegment, arch, binFile),
					},
					RPM: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/%s-%s-1.%s.rpm", pathSegment, arch, pkgFile, semVerTag, rpmArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/%s-%s-1.%s.rpm.sha256sum", pathSegment, arch, pkgFile, semVerTag, rpmArchMap[arch]),
						Text: fmt.Sprintf(`dnf install https://dl.min.io/aistor/minio/%s/linux-%s/%s-%s-1.%s.rpm
%s --version`, pathSegment, arch, pkgFile, semVerTag, rpmArchMap[arch], pkgFile),
					},
					Deb: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/%s_%s_%s.deb", pathSegment, arch, pkgFile, semVerTag, debArchMap[arch]),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/linux-%s/%s_%s_%s.deb.sha256sum", pathSegment, arch, pkgFile, semVerTag, debArchMap[arch]),
						Text: fmt.Sprintf(`wget https://dl.min.io/aistor/minio/%s/linux-%s/%s_%s_%s.deb
dpkg -i %s_%s_%s.deb
%s --version`, pathSegment, arch, pkgFile, semVerTag, debArchMap[arch], pkgFile, semVerTag, debArchMap[arch], pkgFile),
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
			if appName == "ac" {
				d.Subscriptions[subscription].MacOS["AIStor Client"][arch] = downloadJSON{
					Homebrew: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/%s", pathSegment, arch, binFile),
						Text:     `brew install minio/aistor/mc`,
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/%s.sha256sum", pathSegment, arch, binFile),
					},
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/%s", pathSegment, arch, binFile),
						Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/aistor/mc/%s/darwin-%s/%s
chmod +x %s
./%s --version`, pathSegment, arch, binFile, binFile, binFile),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/darwin-%s/%s.sha256sum", pathSegment, arch, binFile),
					},
				}
			}
			if appName == "aistor" {
				d.Subscriptions[subscription].MacOS["AIStor Server"][arch] = downloadJSON{
					Homebrew: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/server/minio/%s/darwin-%s/%s", pathSegment, arch, binFile),
						Checksum: fmt.Sprintf("https://dl.min.io/server/minio/%s/darwin-%s/%s.sha256sum", pathSegment, arch, binFile),
						Text:     `brew install minio/aistor/minio`,
					},

					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/darwin-%s/%s", pathSegment, arch, binFile),
						Text: fmt.Sprintf(`curl --progress-bar -O https://dl.min.io/aistor/minio/%s/darwin-%s/%s
chmod +x %s
./%s --version`, pathSegment, arch, binFile, binFile, binFile),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/darwin-%s/%s.sha256sum", pathSegment, arch, binFile),
					},
				}
			}
		}

		for _, arch := range []string{
			"amd64",
		} {
			if appName == "ac" {
				d.Subscriptions[subscription].Windows["AIStor Client"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/windows-%s/%s.exe", pathSegment, arch, binFile),
						Text: fmt.Sprintf(`Invoke-WebRequest -Uri "https://dl.min.io/aistor/mc/%s/windows-%s/%s.exe" -OutFile "%s.exe"
%s.exe --version`, pathSegment, arch, binFile, binFile, binFile),

						Checksum: fmt.Sprintf("https://dl.min.io/aistor/mc/%s/windows-%s/%s.exe.sha256sum", pathSegment, arch, binFile),
					},
				}
			}

			if appName == "aistor" {
				d.Subscriptions[subscription].Windows["AIStor Server"][arch] = downloadJSON{
					Bin: &dlInfo{
						Download: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/windows-%s/%s.exe", pathSegment, arch, binFile),
						Text: fmt.Sprintf(`Invoke-WebRequest -Uri "https://dl.min.io/aistor/minio/%s/windows-%s/%s.exe" -OutFile "%s.exe"
%s.exe --version`, pathSegment, arch, binFile, binFile, binFile),
						Checksum: fmt.Sprintf("https://dl.min.io/aistor/minio/%s/windows-%s/%s.exe.sha256sum", pathSegment, arch, binFile),
					},
				}
			}
		}
	}
	return d
}

// generateDownloadsJSON is the fallback metadata generator for apps that have no
// dl.min.io download page of their own (memkv, aimem, minfs). It intentionally
// produces an empty document: the packages ship, but there is nothing to link.
func generateDownloadsJSON() downloadsJSON {
	return downloadsJSON{
		Linux:      make(map[string]map[string]downloadJSON),
		MacOS:      make(map[string]map[string]downloadJSON),
		Windows:    make(map[string]map[string]downloadJSON),
		Docker:     make(map[string]map[string]downloadJSON),
		Kubernetes: make(map[string]map[string]downloadJSON),
	}
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
			Bin: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick", arch),
				Text: fmt.Sprintf(`wget https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick
chmod +x sidekick
./sidekick --version`, arch),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick.sha256sum", arch),
			},
			RPM: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick-%s-1.%s.rpm", arch, semVerTag, rpmArchMap[arch]),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick-%s-1.%s.rpm.sha256sum", arch, semVerTag, rpmArchMap[arch]),
				Text: fmt.Sprintf(`wget https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick-%s-1.%s.rpm
sudo dnf install sidekick-%s-1.%s.rpm
sidekick --version`, arch, semVerTag, rpmArchMap[arch], semVerTag, rpmArchMap[arch]),
			},
			Deb: &dlInfo{
				Download: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick_%s_%s.deb", arch, semVerTag, debArchMap[arch]),
				Checksum: fmt.Sprintf("https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick_%s_%s.deb.sha256sum", arch, semVerTag, debArchMap[arch]),
				Text: fmt.Sprintf(`wget https://dl.min.io/aistor/sidekick/release/linux-%s/sidekick_%s_%s.deb
sudo dpkg -i sidekick_%s_%s.deb
sidekick --version`, arch, semVerTag, debArchMap[arch], semVerTag, debArchMap[arch]),
			},
		}
	}

	// Windows: amd64 only
	d.Windows["MinIO Sidekick"]["amd64"] = downloadJSON{
		Bin: &dlInfo{
			Download: "https://dl.min.io/aistor/sidekick/release/windows-amd64/sidekick.exe",
			Text: `Invoke-WebRequest -Uri "https://dl.min.io/aistor/sidekick/release/windows-amd64/sidekick.exe" -OutFile "sidekick.exe"
sidekick.exe --version`,
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
			Text: `Invoke-WebRequest -Uri "https://dl.min.io/aistor/warp/release/windows-amd64/warp.exe" -OutFile "warp.exe"
warp.exe --version`,
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
	// The built artifacts still land in minio-release/ and mc-release/; only the
	// app name was rebranded.
	case "aistor":
		name = "minio"
	case "ac":
		name = "mc"
	}
	return name + "-release"
}

func main() {
	app.Version(getVersionString())
	app.VersionFlag.Short('v')
	app.HelpFlag.Short('h')
	if _, err := app.Parse(os.Args[1:]); err != nil {
		kingpin.Fatalf(err.Error())
	}

	// Validate EDGE tag usage - bidirectional check
	if strings.HasPrefix(*release, "EDGE.") && !*edge {
		kingpin.Fatalf("EDGE-prefixed release tags require --edge flag: %s", *release)
	}
	if *edge && strings.HasPrefix(*release, "RELEASE.") {
		kingpin.Fatalf("--edge flag requires EDGE-prefixed release tag, got: %s", *release)
	}

	if !*noPackages {
		if err := doPackage(*appName, *license, *release, *packager, *deps, *scriptsDir, *binaryName, *packageName); err != nil {
			if !*ignoreMissingArch {
				kingpin.Fatalf(err.Error())
			} else {
				kingpin.Errorf(err.Error())
			}
		}
	}

	// Skip JSON generation if --no-json flag is set
	if *noJSON {
		fmt.Println("Skipping JSON metadata generation (--no-json)")
		return
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
	case "aistor", "ac":
		semVerTag := semVerRelease(*release)
		d = generateEnterpriseDownloadsJSON(semVerTag, *appName, *release, binarySrcName(*appName, *binaryName), pkgName(*appName, *packageName), *edge)
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
		d = generateDownloadsJSON()
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
	LegacyName    string
	Description   string
	OS            string
	Arch          string
	Release       string
	SemVerRelease string

	Scripts       map[string]string
	Deps          map[string][]string
	ExtraContents []extraContent
}

// defaultPkgName is the nfpm package name (and installed command) an app uses by
// default. --package-name overrides it; when it does, this default becomes the
// legacy name (compat symlink + provides/replaces/conflicts).
func defaultPkgName(appName string) string {
	switch appName {
	case "aistor":
		return "minio"
	case "ac":
		return "mcli"
	}
	return appName
}

// defaultBinarySrcName is the base name of the built binary picked up from the
// release dir (src is "<name>.<release>") by default. --binary-name overrides
// it. Independent of the package/install name.
func defaultBinarySrcName(appName string) string {
	switch appName {
	case "aistor":
		return "minio"
	case "ac":
		return "mc"
	}
	return appName
}

// pkgName resolves the package name + installed command: --package-name if set,
// else the per-app default.
func pkgName(appName, packageName string) string {
	if packageName != "" {
		return packageName
	}
	return defaultPkgName(appName)
}

// binarySrcName resolves the source binary base name: --binary-name if set, else
// the per-app default.
func binarySrcName(appName, binaryName string) string {
	if binaryName != "" {
		return binaryName
	}
	return defaultBinarySrcName(appName)
}

// legacyName is the old package/command name to preserve for back-compat: the
// per-app default when --package-name renames it to something else, otherwise ""
// (no rename => no symlink/metadata).
func legacyName(appName, packageName string) string {
	if def := defaultPkgName(appName); pkgName(appName, packageName) != def {
		return def
	}
	return ""
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
	if fields[0] != "RELEASE" && fields[0] != "EDGE" {
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
func doPackage(appName, license, release, packager, deps, scriptsDir, binaryName, packageName string) error {
	var pkgDeps map[string][]string
	if deps != "" {
		var err error
		pkgDeps, err = parseDepsFile(deps)
		if err != nil {
			return err
		}
	}

	var extraContents []extraContent
	if *contentsFile != "" {
		var err error
		extraContents, err = parseContentsFile(*contentsFile)
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

	for _, arch := range pkgArches {
		var buf bytes.Buffer
		err = mtmpl.Execute(&buf, releaseTmpl{
			App: pkgName(appName, packageName),
			License: func() string {
				return license
			}(),
			ReleaseDir: releaseDirName(),
			Binary:     binarySrcName(appName, binaryName),
			LegacyName: legacyName(appName, packageName),
			Description: func() string {
				if appName == "aistor" {
					return `MinIO is a High Performance Object Store.
  It is API compatible with Amazon S3 cloud storage service. Use MinIO to build
  high performance infrastructure for machine learning, analytics and application
  data workloads.`
				}
				if appName == "ac" {
					return `MinIO Client for cloud storage and filesystems`
				}
				if appName == "memkv" {
					return `MemKV is a High Performance Inference Context Memory Store.
  The package bundles the server, the NIXL plugin, the LD_PRELOAD shim,
  and Python plugins for LMCache and sglang.`
				}
				if appName == "aimem" {
					return `AIMem is the workspace, memory, and secrets mount for MinIO AIStor.
  It mounts a MinIO AIStor bucket as a POSIX-correct workspace with
  hydrated agent memory and KMS-backed secrets via MinKMS.`
				}
				if appName == "minfs" {
					return `minfs is the cache-tier mount for MinIO AIStor.
  It serves MinIO AIStor reads through a local on-disk or
  distributed cache cluster.`
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
			Deps: pkgDeps,
			ExtraContents: func() []extraContent {
				expanded := make([]extraContent, len(extraContents))
				for i, c := range extraContents {
					expanded[i] = extraContent{
						Src:  strings.ReplaceAll(c.Src, "${ARCH}", arch),
						Dst:  strings.ReplaceAll(c.Dst, "${ARCH}", arch),
						Type: c.Type,
					}
				}
				return expanded
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
			tgtPath := filepath.Join(releaseDirName(), "linux-"+arch, releasePkg)
			f, err := os.Create(tgtPath)
			if err != nil {
				return err
			}

			{
				// Stable "latest" alias filename in the release dir. On the
				// default (non-rename) path this intentionally keeps the
				// historical alias name (minio.deb, mc.deb, ...) even where it
				// differs from the package name (e.g. mc -> mcli package), since
				// downstream tooling may depend on it. Do NOT switch this to
				// pkgName(); only the renamed path (--package-name set) adopts
				// the new, correct alias name (e.g. aistor.deb, acli.deb).
				aliasBase := func() string {
					if packageName != "" {
						return packageName
					}
					if appName == "aistor" {
						return "minio"
					}
					return appName
				}()

				// target stays a bare filename so the symlink is relative to
				// the release dir and survives being copied or served from
				// elsewhere; only the link path is absolute.
				dir := filepath.Dir(tgtPath)
				link := func(target, name string) error {
					path := filepath.Join(dir, name)
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						return err
					}
					return os.Symlink(target, path)
				}

				if err := link(releasePkg, aliasBase+filepath.Ext(tgtPath)); err != nil {
					return err
				}

				// Under a --package-name rename the package filename changes
				// (minio-*.rpm -> aistor-*.rpm), which would break every
				// published URL built from the old name. Symlink the old alias
				// and the old versioned filename (plus its checksum) onto the
				// new package so existing links keep resolving.
				if legacy := legacyName(appName, packageName); legacy != "" {
					legacyInfo := *info
					legacyInfo.Name = legacy
					legacyPkg := pkg.ConventionalFileName(&legacyInfo)

					if err := link(releasePkg, legacy+filepath.Ext(tgtPath)); err != nil {
						return err
					}
					if legacyPkg != releasePkg {
						if err := link(releasePkg, legacyPkg); err != nil {
							return err
						}
						if err := link(releasePkg+".sha256sum", legacyPkg+".sha256sum"); err != nil {
							return err
						}
					}
				}
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
