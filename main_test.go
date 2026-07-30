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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/goreleaser/nfpm/v2"
	jsoniter "github.com/json-iterator/go"
)

// renderPackageYAML renders the nfpm package template for an app the same way
// doPackage does (honoring the --binary-name/--package-name overrides), so tests
// exercise the real name/symlink/metadata logic.
func renderPackageYAML(t *testing.T, appName, binaryName, packageName string) string {
	t.Helper()
	mtmpl, err := template.New("minio").Parse(tmpl)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	var buf bytes.Buffer
	if err := mtmpl.Execute(&buf, releaseTmpl{
		App:           pkgName(appName, packageName),
		License:       "Test License",
		ReleaseDir:    defaultPkgName(appName) + "-release",
		Binary:        binarySrcName(appName, binaryName),
		LegacyName:    legacyName(appName, packageName),
		Description:   "Test package",
		OS:            "linux",
		Arch:          "amd64",
		Release:       "RELEASE.2025-03-12T00-00-00Z",
		SemVerRelease: "20250312000000.0.0",
	}); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	// The rendered document must be valid nfpm config.
	rendered := buf.Bytes()
	if _, err := nfpm.Parse(bytes.NewReader(rendered)); err != nil {
		t.Fatalf("nfpm.Parse failed for %s: %v\n---\n%s", appName, err, rendered)
	}
	return buf.String()
}

// TestPackageTemplateRename verifies the rename + back-compat symlink +
// provides/replaces/conflicts for both enterprise products.
func TestPackageTemplateRename(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		binaryName  string // --binary-name
		packageName string // --package-name
		wantPkg     string
		wantInstall string
		wantSrcFile string // <name>.<release> source read from release dir
		wantLegacy  string // symlink at + provides/replaces/conflicts
		wantService bool
	}{
		{
			name: "aistor -> aistor", appName: "aistor",
			binaryName: "aistor", packageName: "aistor",
			wantPkg: "aistor", wantInstall: "/usr/local/bin/aistor",
			wantSrcFile: "aistor", wantLegacy: "minio", wantService: true,
		},
		{
			name: "ac -> acli (binary ac)", appName: "ac",
			binaryName: "ac", packageName: "acli",
			wantPkg: "acli", wantInstall: "/usr/local/bin/acli",
			wantSrcFile: "ac", wantLegacy: "mcli", wantService: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderPackageYAML(t, tt.appName, tt.binaryName, tt.packageName)

			if !strings.Contains(out, `name: "`+tt.wantPkg+`"`) {
				t.Errorf("package name: want %s\n%s", tt.wantPkg, out)
			}
			if !strings.Contains(out, "dst: "+tt.wantInstall) {
				t.Errorf("install path: want %s\n%s", tt.wantInstall, out)
			}
			if !strings.Contains(out, "linux-amd64/"+tt.wantSrcFile+".RELEASE.2025-03-12T00-00-00Z") {
				t.Errorf("source file: want %s.<rel>\n%s", tt.wantSrcFile, out)
			}
			// Back-compat symlink /usr/local/bin/<legacy> -> <pkg>.
			if !containsSymlink(out, tt.wantPkg, "/usr/local/bin/"+tt.wantLegacy) {
				t.Errorf("symlink: want /usr/local/bin/%s -> %s\n%s", tt.wantLegacy, tt.wantPkg, out)
			}
			// Supersede metadata for the old package.
			for _, field := range []string{"provides", "replaces", "conflicts"} {
				if !containsListEntry(out, field, tt.wantLegacy) {
					t.Errorf("%s: want %s\n%s", field, tt.wantLegacy, out)
				}
			}
			hasSvc := strings.Contains(out, "dst: /lib/systemd/system/minio.service")
			if hasSvc != tt.wantService {
				t.Errorf("minio.service presence = %v, want %v", hasSvc, tt.wantService)
			}
		})
	}
}

// TestPackageTemplateNoRenameDefaults ensures that without the override flags the
// output is byte-for-byte the historical behavior: package under the default
// name, no compat symlink, no supersede metadata. This protects packaging of
// pre-rename releases (e.g. old-release hotfixes).
func TestPackageTemplateNoRenameDefaults(t *testing.T) {
	tests := []struct {
		name       string
		appName    string
		wantName   string
		wantBinDst string
		wantSrc    string
		wantSvc    bool
	}{
		{"aistor", "aistor", "minio", "/usr/local/bin/minio", "minio", true},
		{"ac", "ac", "mcli", "/usr/local/bin/mcli", "mc", false},
		{"sidekick", "sidekick", "sidekick", "/usr/local/bin/sidekick", "sidekick", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderPackageYAML(t, tt.appName, "", "")
			if !strings.Contains(out, `name: "`+tt.wantName+`"`) {
				t.Errorf("expected name %s, got:\n%s", tt.wantName, out)
			}
			if !strings.Contains(out, "dst: "+tt.wantBinDst) {
				t.Errorf("expected binary at %s, got:\n%s", tt.wantBinDst, out)
			}
			if !strings.Contains(out, "linux-amd64/"+tt.wantSrc+".RELEASE.2025-03-12T00-00-00Z") {
				t.Errorf("expected src %s.<rel>, got:\n%s", tt.wantSrc, out)
			}
			if strings.Contains(out, "type: symlink") {
				t.Errorf("%s should not emit a compat symlink, got:\n%s", tt.appName, out)
			}
			for _, field := range []string{"provides:", "replaces:", "conflicts:"} {
				if strings.Contains(out, field) {
					t.Errorf("%s should not emit %s, got:\n%s", tt.appName, field, out)
				}
			}
			hasSvc := strings.Contains(out, "dst: /lib/systemd/system/minio.service")
			if hasSvc != tt.wantSvc {
				t.Errorf("%s minio.service presence = %v, want %v", tt.appName, hasSvc, tt.wantSvc)
			}
		})
	}
}

// runDoPackage builds real packages in a temp dir. It stages a source binary for
// every arch in pkgArches, so no arch is silently skipped, and returns the
// per-arch release dirs keyed by arch.
func runDoPackage(t *testing.T, app, pkgOverride string) map[string]string {
	t.Helper()
	const rel = "RELEASE.2025-03-12T00-00-00Z"

	dir := t.TempDir()
	t.Chdir(dir)

	oldApp, oldDir, oldIgnore := *appName, *releaseDir, *ignoreMissingArch
	t.Cleanup(func() { *appName, *releaseDir, *ignoreMissingArch = oldApp, oldDir, oldIgnore })
	// No --ignore: every arch must package cleanly from the staged fixtures.
	*appName, *releaseDir, *ignoreMissingArch = app, "", false

	// Stage into the same directory doPackage reads from.
	relDir := releaseDirName()
	out := make(map[string]string, len(pkgArches))
	for _, arch := range pkgArches {
		archDir := filepath.Join(dir, relDir, "linux-"+arch)
		if err := os.MkdirAll(archDir, 0o750); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(archDir, defaultBinarySrcName(app)+"."+rel)
		if err := os.WriteFile(src, []byte("binary"), 0o600); err != nil {
			t.Fatal(err)
		}
		out[arch] = archDir
	}
	// aistor packages embed a systemd unit that is not vendored in this repo.
	if err := os.WriteFile(filepath.Join(dir, "minio.service"), []byte("[Unit]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := doPackage(app, "Test License", rel, "deb,rpm,apk", "", "./", "", pkgOverride); err != nil {
		t.Fatalf("doPackage(%s, pkg=%q): %v", app, pkgOverride, err)
	}
	return out
}

// pkgSuffix maps an arch to the per-format package filename suffixes nfpm
// produces, so the link assertions cover every arch pkgArches builds.
func pkgSuffix(arch string) map[string]string {
	return map[string]string{
		"rpm": "-20250312000000.0.0-1." + rpmArchMap[arch] + ".rpm",
		"deb": "_20250312000000.0.0_" + debArchMap[arch] + ".deb",
		"apk": "_20250312000000.0.0_" + rpmArchMap[arch] + ".apk",
	}
}

// TestPackageRenameKeepsOldLinks pins the compatibility guarantee for a
// --package-name rename: the packages ship under the new name, and every
// filename an already-published URL could reference (the "latest" alias, the
// versioned package, its checksum) still resolves via a symlink. Checked for
// every arch, for both renamed apps.
func TestPackageRenameKeepsOldLinks(t *testing.T) {
	for _, tt := range []struct {
		app, pkgOverride, legacy string
	}{
		{app: "aistor", pkgOverride: "aistor", legacy: "minio"},
		{app: "ac", pkgOverride: "acli", legacy: "mcli"},
	} {
		t.Run(tt.app, func(t *testing.T) {
			out := runDoPackage(t, tt.app, tt.pkgOverride)

			for arch, dir := range out {
				sfx := pkgSuffix(arch)

				// The renamed package is the only real file.
				for _, format := range []string{"rpm", "deb", "apk"} {
					name := tt.pkgOverride + sfx[format]
					fi, err := os.Lstat(filepath.Join(dir, name))
					if err != nil {
						t.Fatalf("%s/%s missing: %v", arch, name, err)
					}
					if fi.Mode()&os.ModeSymlink != 0 {
						t.Errorf("%s/%s should be a regular file, not a symlink", arch, name)
					}
				}

				var links []string
				for _, format := range []string{"rpm", "deb", "apk"} {
					links = append(links,
						// New-style "latest" alias.
						tt.pkgOverride+"."+format,
						// Old-style URLs that must keep working.
						tt.legacy+"."+format,
						tt.legacy+sfx[format],
						tt.legacy+sfx[format]+".sha256sum",
					)
				}
				for _, name := range links {
					p := filepath.Join(dir, name)
					fi, err := os.Lstat(p)
					if err != nil {
						t.Errorf("%s/%s missing: %v", arch, name, err)
						continue
					}
					if fi.Mode()&os.ModeSymlink == 0 {
						t.Errorf("%s/%s should be a symlink", arch, name)
					}
					// Relative target keeps the link valid if the dir moves.
					if target, err := os.Readlink(p); err == nil && filepath.IsAbs(target) {
						t.Errorf("%s/%s target should be relative, got %s", arch, name, target)
					}
					if _, err := os.Stat(p); err != nil {
						t.Errorf("%s/%s does not resolve: %v", arch, name, err)
					}
				}
			}
		})
	}
}

// TestPackageDefaultsEmitNoLegacyLinks is the other half of the contract: with
// no rename there is nothing to alias, so each arch dir must hold exactly the
// historical set of files.
func TestPackageDefaultsEmitNoLegacyLinks(t *testing.T) {
	for _, tt := range []struct {
		app, pkg, alias string
	}{
		// aistor keeps the historical minio.* alias; ac's alias follows the
		// app name, as it did pre-rename (mc-enterprise.* -> ac.*), while the
		// package itself stays mcli.
		{app: "aistor", pkg: "minio", alias: "minio"},
		{app: "ac", pkg: "mcli", alias: "ac"},
	} {
		t.Run(tt.app, func(t *testing.T) {
			out := runDoPackage(t, tt.app, "")

			for arch, dir := range out {
				sfx := pkgSuffix(arch)

				want := map[string]bool{
					defaultBinarySrcName(tt.app) + ".RELEASE.2025-03-12T00-00-00Z": true,
				}
				for _, format := range []string{"rpm", "deb", "apk"} {
					want[tt.pkg+sfx[format]] = true
					want[tt.pkg+sfx[format]+".sha256sum"] = true
					want[tt.alias+"."+format] = true
				}

				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]bool{}
				for _, e := range entries {
					got[e.Name()] = true
				}
				for name := range want {
					if !got[name] {
						t.Errorf("%s: missing %s", arch, name)
					}
					delete(got, name)
				}
				for name := range got {
					t.Errorf("%s: unexpected extra file %s", arch, name)
				}
			}
		})
	}
}

// TestEnterpriseDownloadsJSONRename checks the downloads metadata uses the
// renamed filenames while keeping the dl paths unchanged.
func TestEnterpriseDownloadsJSONRename(t *testing.T) {
	semVer := "20250312000000.0.0"
	rel := "RELEASE.2025-03-12T00-00-00Z"

	t.Run("aistor", func(t *testing.T) {
		d := generateEnterpriseDownloadsJSON(semVer, "aistor", rel, "aistor", "aistor", false)
		lin := d.Subscriptions["Enterprise"].Linux["AIStor Server"]["amd64"]
		if lin.Bin.Download != "https://dl.min.io/aistor/minio/release/linux-amd64/aistor" {
			t.Errorf("bin download: %s", lin.Bin.Download)
		}
		if !strings.Contains(lin.Deb.Download, "/aistor/minio/release/linux-amd64/aistor_") || !strings.HasSuffix(lin.Deb.Download, "_amd64.deb") {
			t.Errorf("deb download: %s", lin.Deb.Download)
		}
		if !strings.Contains(lin.RPM.Download, "/aistor/minio/release/linux-amd64/aistor-") {
			t.Errorf("rpm download: %s", lin.RPM.Download)
		}
	})

	t.Run("ac", func(t *testing.T) {
		d := generateEnterpriseDownloadsJSON(semVer, "ac", rel, "ac", "acli", false)
		lin := d.Subscriptions["Enterprise"].Linux["AIStor Client"]["amd64"]
		if lin.Bin.Download != "https://dl.min.io/aistor/mc/release/linux-amd64/ac" {
			t.Errorf("bin download: %s", lin.Bin.Download)
		}
		if !strings.Contains(lin.Deb.Download, "/aistor/mc/release/linux-amd64/acli_") {
			t.Errorf("deb download: %s", lin.Deb.Download)
		}
		if !strings.Contains(lin.RPM.Download, "/aistor/mc/release/linux-amd64/acli-") {
			t.Errorf("rpm download: %s", lin.RPM.Download)
		}
	})
}

// containsListEntry checks the rendered YAML has a top-level key `field:` whose
// list contains `- value` (e.g. provides:\n- minio).
func containsListEntry(out, field, value string) bool {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != field+":" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			s := strings.TrimSpace(lines[j])
			if s == "" {
				continue
			}
			if !strings.HasPrefix(s, "- ") {
				break // end of this list
			}
			if s == "- "+value {
				return true
			}
		}
	}
	return false
}

// containsSymlink checks the rendered YAML has a content entry that is a symlink
// with the given src (target) and dst (link path).
func containsSymlink(out, src, dst string) bool {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		// src entries are YAML list items ("- src: ...").
		if strings.TrimSpace(l) != "- src: "+src {
			continue
		}
		// Look at the next two lines for dst + type: symlink.
		var haveDst, haveType bool
		for j := i + 1; j < len(lines) && j <= i+2; j++ {
			s := strings.TrimSpace(lines[j])
			if s == "dst: "+dst {
				haveDst = true
			}
			if s == "type: symlink" {
				haveType = true
			}
		}
		if haveDst && haveType {
			return true
		}
	}
	return false
}

// TestSemVerRelease tests the conversion from release tags to semver format
func TestSemVerRelease(t *testing.T) {
	tests := []struct {
		name        string
		releaseTag  string
		expected    string
		expectPanic bool
	}{
		{
			name:       "Standard release tag",
			releaseTag: "RELEASE.2025-03-12T00-00-00Z",
			expected:   "20250312000000.0.0",
		},
		{
			name:       "Release with hotfix",
			releaseTag: "RELEASE.2025-03-12T00-00-00Z.hotfix.1",
			expected:   "20250312000000.0.0.hotfix.1",
		},
		{
			name:       "EDGE release tag",
			releaseTag: "EDGE.2025-10-10T05-28-23Z",
			expected:   "20251010052823.0.0",
		},
		{
			name:       "EDGE release with hotfix",
			releaseTag: "EDGE.2025-10-10T05-28-23Z.hotfix.2",
			expected:   "20251010052823.0.0.hotfix.2",
		},
		{
			name:        "Invalid format - no RELEASE/EDGE prefix",
			releaseTag:  "2025-03-12T00-00-00Z",
			expectPanic: true,
		},
		{
			name:        "Invalid format - too few fields",
			releaseTag:  "RELEASE",
			expectPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectPanic {
				defer func() {
					if r := recover(); r == nil {
						t.Errorf("Expected panic but got none")
					}
				}()
			}

			result := semVerRelease(tt.releaseTag)
			if !tt.expectPanic && result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// TestGenerateEnterpriseDownloadsJSON tests enterprise JSON generation
func TestGenerateEnterpriseDownloadsJSON(t *testing.T) {
	semVerTag := "20250312000000.0.0"
	releaseTag := "RELEASE.2025-03-12T00-00-00Z"

	t.Run("MinIO Enterprise Release", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "aistor", releaseTag, "minio", "minio", false)

		// Verify structure
		if result.Subscriptions == nil {
			t.Fatal("Subscriptions is nil")
		}
		if _, ok := result.Subscriptions["Enterprise"]; !ok {
			t.Fatal("Enterprise subscription not found")
		}

		// Verify Linux AIStor Server has required fields
		linuxData := result.Subscriptions["Enterprise"].Linux["AIStor Server"]["amd64"]
		if linuxData.Bin == nil {
			t.Error("Binary download info missing")
		}
		if linuxData.RPM == nil {
			t.Error("RPM download info missing")
		}
		if linuxData.Deb == nil {
			t.Error("DEB download info missing")
		}

		// Verify release path
		if linuxData.Bin.Download != "" && !strings.Contains(linuxData.Bin.Download, "/release/") {
			t.Error("Binary download should contain '/release/' path")
		}
	})

	t.Run("MinIO Enterprise EDGE", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "aistor", releaseTag, "minio", "minio", true)

		// Verify EDGE path
		linuxData := result.Subscriptions["Enterprise"].Linux["AIStor Server"]["amd64"]
		if !strings.Contains(linuxData.Bin.Download, "/edge/") {
			t.Error("EDGE release should contain '/edge/' path")
		}
		if !strings.Contains(linuxData.RPM.Download, "/edge/") {
			t.Error("EDGE RPM should contain '/edge/' path")
		}
		if !strings.Contains(linuxData.Deb.Download, "/edge/") {
			t.Error("EDGE DEB should contain '/edge/' path")
		}
	})

	t.Run("Docker tags use release version", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "aistor", releaseTag, "minio", "minio", false)

		dockerData := result.Subscriptions["Enterprise"].Docker["AIStor Server"]["amd64"]
		if dockerData.Podman == nil {
			t.Fatal("Docker Podman info missing")
		}
		if !strings.Contains(dockerData.Podman.Text, releaseTag) {
			t.Errorf("Docker should use release tag %s, got: %s", releaseTag, dockerData.Podman.Text)
		}
		if strings.Contains(dockerData.Podman.Text, ":latest") {
			t.Error("Docker should NOT use :latest tag")
		}
	})

	t.Run("MC Enterprise", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "ac", releaseTag, "mc", "mcli", false)

		linuxData := result.Subscriptions["Enterprise"].Linux["AIStor Client"]["amd64"]
		if linuxData.Bin == nil {
			t.Error("Binary download info missing for ac")
		}

		// Verify mc paths
		if !strings.Contains(linuxData.Bin.Download, "/aistor/mc/") {
			t.Error("MC should use /aistor/mc/ path")
		}
	})
}

// TestGenerateDownloadsJSON checks the fallback generator still emits a valid,
// empty document for apps without a dl.min.io download page (memkv, aimem,
// minfs) so their downloads-<app>.json marshals cleanly.
func TestGenerateDownloadsJSON(t *testing.T) {
	result := generateDownloadsJSON()

	buf, err := jsoniter.ConfigCompatibleWithStandardLibrary.Marshal(&result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for name, section := range map[string]map[string]map[string]downloadJSON{
		"Linux":      result.Linux,
		"MacOS":      result.MacOS,
		"Windows":    result.Windows,
		"Docker":     result.Docker,
		"Kubernetes": result.Kubernetes,
	} {
		if len(section) != 0 {
			t.Errorf("expected %s to be empty, got %s", name, buf)
		}
	}
}

// TestGenerateSidekickDownloadsJSON tests sidekick JSON generation
func TestGenerateSidekickDownloadsJSON(t *testing.T) {
	semVerTag := "20250312000000.0.0"
	releaseTag := "RELEASE.2025-03-12T00-00-00Z"

	result := generateSidekickDownloadsJSON(semVerTag, releaseTag)

	// Verify Linux and Windows support, but not MacOS
	if result.MacOS != nil {
		t.Error("Sidekick should not have MacOS support")
	}
	if result.Windows == nil {
		t.Error("Sidekick should have Windows support")
	}

	// Verify only amd64 and arm64 for Linux
	if _, ok := result.Linux["MinIO Sidekick"]["amd64"]; !ok {
		t.Error("amd64 architecture missing")
	}
	if _, ok := result.Linux["MinIO Sidekick"]["arm64"]; !ok {
		t.Error("arm64 architecture missing")
	}
	if _, ok := result.Linux["MinIO Sidekick"]["ppc64le"]; ok {
		t.Error("ppc64le should not be supported for sidekick")
	}

	// Verify binary downloads and packages on Linux
	linuxData := result.Linux["MinIO Sidekick"]["amd64"]
	if linuxData.Bin == nil {
		t.Error("Sidekick should have binary downloads on Linux")
	}
	if linuxData.Bin != nil {
		if linuxData.Bin.Download != "https://dl.min.io/aistor/sidekick/release/linux-amd64/sidekick" {
			t.Errorf("Incorrect Linux binary download URL: %s", linuxData.Bin.Download)
		}
		if linuxData.Bin.Checksum != "https://dl.min.io/aistor/sidekick/release/linux-amd64/sidekick.sha256sum" {
			t.Errorf("Incorrect Linux binary checksum URL: %s", linuxData.Bin.Checksum)
		}
	}
	if linuxData.RPM == nil {
		t.Error("Sidekick should have RPM packages")
	}
	if linuxData.Deb == nil {
		t.Error("Sidekick should have DEB packages")
	}

	// Verify Windows amd64 binary download
	if _, ok := result.Windows["MinIO Sidekick"]["amd64"]; !ok {
		t.Error("Windows amd64 architecture missing")
	}
	windowsData := result.Windows["MinIO Sidekick"]["amd64"]
	if windowsData.Bin == nil {
		t.Error("Sidekick should have binary download for Windows")
	}
	if windowsData.Bin.Download != "https://dl.min.io/aistor/sidekick/release/windows-amd64/sidekick.exe" {
		t.Errorf("Incorrect Windows download URL: %s", windowsData.Bin.Download)
	}
	if windowsData.Bin.Checksum != "https://dl.min.io/aistor/sidekick/release/windows-amd64/sidekick.exe.sha256sum" {
		t.Errorf("Incorrect Windows checksum URL: %s", windowsData.Bin.Checksum)
	}
}

// TestGenerateWarpDownloadsJSON tests warp JSON generation
func TestGenerateWarpDownloadsJSON(t *testing.T) {
	version := "0.4.3"     // Without 'v' prefix
	releaseTag := "v0.4.3" // With 'v' prefix

	result := generateWarpDownloadsJSON(version, releaseTag)

	// Verify cross-platform support
	if result.Linux == nil {
		t.Error("Warp should support Linux")
	}
	if result.MacOS == nil {
		t.Error("Warp should support MacOS")
	}
	if result.Windows == nil {
		t.Error("Warp should support Windows")
	}

	// Verify Linux architectures (amd64, arm64 only)
	if _, ok := result.Linux["MinIO Warp"]["amd64"]; !ok {
		t.Error("amd64 architecture missing for Linux")
	}
	if _, ok := result.Linux["MinIO Warp"]["arm64"]; !ok {
		t.Error("arm64 architecture missing for Linux")
	}
	if _, ok := result.Linux["MinIO Warp"]["ppc64le"]; ok {
		t.Error("ppc64le should not be supported for warp")
	}

	// Verify MacOS only arm64
	if _, ok := result.MacOS["MinIO Warp"]["arm64"]; !ok {
		t.Error("arm64 architecture missing for MacOS")
	}
	if _, ok := result.MacOS["MinIO Warp"]["amd64"]; ok {
		t.Error("amd64 should not be supported for MacOS warp")
	}

	// Verify Windows only amd64
	if _, ok := result.Windows["MinIO Warp"]["amd64"]; !ok {
		t.Error("amd64 architecture missing for Windows")
	}

	// Verify version format in URLs (without 'v' prefix)
	linuxData := result.Linux["MinIO Warp"]["amd64"]
	if strings.Contains(linuxData.RPM.Download, "v0.4.3") {
		t.Error("RPM URL should not contain 'v' prefix")
	}
	if !strings.Contains(linuxData.RPM.Download, "0.4.3") {
		t.Error("RPM URL should contain version without 'v' prefix")
	}
}

// TestReleaseDirName tests release directory name logic
func TestReleaseDirName(t *testing.T) {
	tests := []struct {
		name     string
		appName  string
		expected string
	}{
		{
			name:     "aistor",
			appName:  "aistor",
			expected: "minio-release",
		},
		{
			name:     "ac",
			appName:  "ac",
			expected: "mc-release",
		},
		{
			name:     "sidekick",
			appName:  "sidekick",
			expected: "sidekick-release",
		},
		{
			name:     "warp",
			appName:  "warp",
			expected: "warp-release",
		},
		{
			name:     "memkv",
			appName:  "memkv",
			expected: "memkv-release",
		},
		{
			name:     "aimem",
			appName:  "aimem",
			expected: "aimem-release",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore global state
			oldAppName := appName
			oldReleaseDir := releaseDir
			defer func() {
				appName = oldAppName
				releaseDir = oldReleaseDir
			}()

			// Set test values
			*appName = tt.appName
			*releaseDir = ""

			result := releaseDirName()
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// TestArchitectureMappings tests RPM and DEB architecture mappings
func TestArchitectureMappings(t *testing.T) {
	t.Run("RPM arch mapping", func(t *testing.T) {
		if rpmArchMap["amd64"] != "x86_64" {
			t.Error("amd64 should map to x86_64 for RPM")
		}
		if rpmArchMap["arm64"] != "aarch64" {
			t.Error("arm64 should map to aarch64 for RPM")
		}
	})

	t.Run("DEB arch mapping", func(t *testing.T) {
		if debArchMap["amd64"] != "amd64" {
			t.Error("amd64 should map to amd64 for DEB")
		}
		if debArchMap["arm64"] != "arm64" {
			t.Error("arm64 should map to arm64 for DEB")
		}
	})
}

// TestPkgArches pins the packaged arch set. Every supported app (aistor, ac,
// sidekick, warp, memkv, aimem, minfs) is amd64+arm64 only; ppc64le went away
// with the community minio/mc packages and must not come back silently.
func TestPkgArches(t *testing.T) {
	want := []string{"amd64", "arm64"}
	if len(pkgArches) != len(want) {
		t.Fatalf("pkgArches = %v, want %v", pkgArches, want)
	}
	for i, arch := range want {
		if pkgArches[i] != arch {
			t.Errorf("pkgArches[%d] = %q, want %q", i, pkgArches[i], arch)
		}
	}
}

// TestURLPathStructure validates URL structure for different release types
func TestURLPathStructure(t *testing.T) {
	semVerTag := "20250312000000.0.0"
	releaseTag := "RELEASE.2025-03-12T00-00-00Z"

	t.Run("Enterprise MinIO uses /aistor/minio/release/", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "aistor", releaseTag, "minio", "minio", false)
		binURL := result.Subscriptions["Enterprise"].Linux["AIStor Server"]["amd64"].Bin.Download
		if !strings.HasPrefix(binURL, "https://dl.min.io/aistor/minio/release/") {
			t.Errorf("Unexpected URL structure: %s", binURL)
		}
	})

	t.Run("Enterprise EDGE uses /aistor/minio/edge/", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "aistor", releaseTag, "minio", "minio", true)
		binURL := result.Subscriptions["Enterprise"].Linux["AIStor Server"]["amd64"].Bin.Download
		if !strings.HasPrefix(binURL, "https://dl.min.io/aistor/minio/edge/") {
			t.Errorf("Unexpected EDGE URL structure: %s", binURL)
		}
	})

	t.Run("Sidekick uses /aistor/sidekick/release/", func(t *testing.T) {
		result := generateSidekickDownloadsJSON(semVerTag, releaseTag)
		rpmURL := result.Linux["MinIO Sidekick"]["amd64"].RPM.Download
		if !strings.HasPrefix(rpmURL, "https://dl.min.io/aistor/sidekick/release/") {
			t.Errorf("Unexpected URL structure: %s", rpmURL)
		}
	})

	t.Run("Warp uses /aistor/warp/release/", func(t *testing.T) {
		result := generateWarpDownloadsJSON("0.4.3", "v0.4.3")
		binURL := result.Linux["MinIO Warp"]["amd64"].Bin.Download
		if !strings.HasPrefix(binURL, "https://dl.min.io/aistor/warp/release/") {
			t.Errorf("Unexpected URL structure: %s", binURL)
		}
	})
}
