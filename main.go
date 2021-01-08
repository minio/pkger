package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/alecthomas/kingpin"

	"github.com/goreleaser/nfpm/v2"
	_ "github.com/goreleaser/nfpm/v2/apk"
	_ "github.com/goreleaser/nfpm/v2/deb"
	_ "github.com/goreleaser/nfpm/v2/rpm"
)

// nolint: gochecknoglobals
var (
	version        = "v1.0"
	releaseMatcher = regexp.MustCompile(`[0-9]`)

	app     = kingpin.New("pkger", "debian and rpms for minio")
	appName = app.Flag("appName", "which package to build deb and rpm for").
		Default("minio").
		Short('a').
		String()
	release = app.Flag("release", "current release tag").
		Default("").
		Short('r').
		String()
	packager = app.Flag("packager", "which packager implementation to use").
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
			SemVerRelease: semVerRelease(release),
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

			tgtPath := filepath.Join(appName+"-release", "linux-"+arch, pkg.ConventionalFileName(info))
			f, err := os.Create(tgtPath)
			if err != nil {
				return err
			}

			info.Target = tgtPath

			err = pkg.Package(info, f)
			_ = f.Close()
			if err != nil {
				os.Remove(tgtPath)
				return err
			}

			fmt.Printf("created package: %s\n", tgtPath)
		}
	}
	return nil
}
