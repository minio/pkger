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
	"strings"
	"testing"
)

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
		result := generateEnterpriseDownloadsJSON(semVerTag, "minio-enterprise", releaseTag, false)

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
		result := generateEnterpriseDownloadsJSON(semVerTag, "minio-enterprise", releaseTag, true)

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
		result := generateEnterpriseDownloadsJSON(semVerTag, "minio-enterprise", releaseTag, false)

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
		result := generateEnterpriseDownloadsJSON(semVerTag, "mc-enterprise", releaseTag, false)

		linuxData := result.Subscriptions["Enterprise"].Linux["AIStor Client"]["amd64"]
		if linuxData.Bin == nil {
			t.Error("Binary download info missing for mc-enterprise")
		}

		// Verify mc paths
		if !strings.Contains(linuxData.Bin.Download, "/aistor/mc/") {
			t.Error("MC should use /aistor/mc/ path")
		}
	})
}

// TestGenerateDownloadsJSON tests community JSON generation
func TestGenerateDownloadsJSON(t *testing.T) {
	semVerTag := "20250312000000.0.0"

	t.Run("MinIO Community", func(t *testing.T) {
		result := generateDownloadsJSON(semVerTag, "minio")

		// Verify Linux has all architectures
		if _, ok := result.Linux["MinIO Server"]["amd64"]; !ok {
			t.Error("amd64 architecture missing")
		}
		if _, ok := result.Linux["MinIO Server"]["arm64"]; !ok {
			t.Error("arm64 architecture missing")
		}
		if _, ok := result.Linux["MinIO Server"]["ppc64le"]; !ok {
			t.Error("ppc64le architecture missing")
		}

		// Verify RPM architecture mapping
		rpmData := result.Linux["MinIO Server"]["amd64"].RPM
		if !strings.Contains(rpmData.Download, "x86_64.rpm") {
			t.Error("RPM should use x86_64 architecture for amd64")
		}

		// Verify DEB architecture mapping
		debData := result.Linux["MinIO Server"]["amd64"].Deb
		if !strings.Contains(debData.Download, "_amd64.deb") {
			t.Error("DEB should use amd64 architecture")
		}
	})

	t.Run("MC Community", func(t *testing.T) {
		result := generateDownloadsJSON(semVerTag, "mc")

		// Verify package name is mcli not mc
		rpmData := result.Linux["MinIO Client"]["amd64"].RPM
		if !strings.Contains(rpmData.Download, "mcli-") {
			t.Error("MC packages should be named 'mcli'")
		}
	})
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
	version := "0.4.3"      // Without 'v' prefix
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
			name:     "minio-enterprise",
			appName:  "minio-enterprise",
			expected: "minio-release",
		},
		{
			name:     "mc-enterprise",
			appName:  "mc-enterprise",
			expected: "mc-release",
		},
		{
			name:     "minio",
			appName:  "minio",
			expected: "minio-release",
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

// TestURLPathStructure validates URL structure for different release types
func TestURLPathStructure(t *testing.T) {
	semVerTag := "20250312000000.0.0"
	releaseTag := "RELEASE.2025-03-12T00-00-00Z"

	t.Run("Community MinIO uses /server/minio/release/", func(t *testing.T) {
		result := generateDownloadsJSON(semVerTag, "minio")
		binURL := result.Linux["MinIO Server"]["amd64"].Bin.Download
		if !strings.HasPrefix(binURL, "https://dl.min.io/server/minio/release/") {
			t.Errorf("Unexpected URL structure: %s", binURL)
		}
	})

	t.Run("Enterprise MinIO uses /aistor/minio/release/", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "minio-enterprise", releaseTag, false)
		binURL := result.Subscriptions["Enterprise"].Linux["AIStor Server"]["amd64"].Bin.Download
		if !strings.HasPrefix(binURL, "https://dl.min.io/aistor/minio/release/") {
			t.Errorf("Unexpected URL structure: %s", binURL)
		}
	})

	t.Run("Enterprise EDGE uses /aistor/minio/edge/", func(t *testing.T) {
		result := generateEnterpriseDownloadsJSON(semVerTag, "minio-enterprise", releaseTag, true)
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
