// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/pkg/errors"
)

// ManifestName is the file Microsoft Teams requires at the root of an app package.
const ManifestName = "manifest.json"

// maxManifestPackageBytes caps what is read out of an app package; a real one is tens of kilobytes.
const maxManifestPackageBytes = 8 << 20

// teamsManifest models only the manifest fields the doctor checks.
type teamsManifest struct {
	Schema          string `json:"$schema"`
	ManifestVersion string `json:"manifestVersion"`
	Version         string `json:"version"`
	ID              string `json:"id"`

	Icons struct {
		Color   string `json:"color"`
		Outline string `json:"outline"`
	} `json:"icons"`

	StaticTabs []struct {
		EntityID   string `json:"entityId"`
		ContentURL string `json:"contentUrl"`
	} `json:"staticTabs"`

	ValidDomains []string `json:"validDomains"`

	WebApplicationInfo struct {
		ID       string `json:"id"`
		Resource string `json:"resource"`
	} `json:"webApplicationInfo"`

	Authorization struct {
		Permissions struct {
			ResourceSpecific []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"resourceSpecific"`
		} `json:"permissions"`
	} `json:"authorization"`

	SourcePath string `json:"-"`

	// FromPackage records that the source was a zip app package rather than a bare manifest.json.
	FromPackage bool `json:"-"`

	// packageFiles holds the package entries by name. Nil for a bare manifest.
	packageFiles map[string][]byte
}

// loadManifest reads a Teams app manifest from either the .zip app package the
// plugin serves or a bare manifest.json, detected by content since downloads get renamed.
func loadManifest(path string) (*teamsManifest, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s", path)
	}

	if len(data) > maxManifestPackageBytes {
		return nil, errors.Errorf("%s is %d bytes, larger than the %d byte limit for an app package", path, len(data), maxManifestPackageBytes)
	}

	if isZip(data) {
		return loadManifestPackage(path, data)
	}

	manifest, err := decodeManifest(data)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse %s as a Teams app manifest", path)
	}
	manifest.SourcePath = path

	return manifest, nil
}

// isZip reports whether the data begins with the PKZip local file header magic.
func isZip(data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04"))
}

// loadManifestPackage extracts and parses the manifest from a zip app package,
// keeping the other entries so the package can be checked for completeness.
func loadManifestPackage(path string, data []byte) (*teamsManifest, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open %s as an app package", path)
	}

	files := make(map[string][]byte, len(reader.File))

	// The limit applies to the running total, not per entry.
	var total int

	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}

		contents, readErr := readZipEntry(entry, maxManifestPackageBytes-total)
		if readErr != nil {
			return nil, errors.Wrapf(readErr, "failed to read %s from %s", entry.Name, path)
		}

		total += len(contents)
		files[entry.Name] = contents
	}

	raw, ok := files[ManifestName]
	if !ok {
		return nil, errors.Errorf("%s is an app package but contains no %s", path, ManifestName)
	}

	manifest, err := decodeManifest(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse %s from %s", ManifestName, path)
	}

	manifest.SourcePath = path
	manifest.FromPackage = true
	manifest.packageFiles = files

	return manifest, nil
}

// readZipEntry decompresses one package entry, refusing anything that inflates
// past the remaining budget for the package as a whole.
func readZipEntry(entry *zip.File, remaining int) ([]byte, error) {
	if remaining < 0 {
		remaining = 0
	}

	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	// Don't trust the declared uncompressed size.
	contents, err := io.ReadAll(io.LimitReader(rc, int64(remaining)+1))
	if err != nil {
		return nil, err
	}

	if len(contents) > remaining {
		return nil, errors.Errorf("entry %s expands the package beyond the %d byte limit", entry.Name, maxManifestPackageBytes)
	}

	return contents, nil
}

func decodeManifest(raw []byte) (*teamsManifest, error) {
	var manifest teamsManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// packageFile returns a package entry by the relative path the manifest names.
// Teams manifests reference icons with forward slashes; zip entries use the
// same separator, but a leading "./" is tolerated.
func (m *teamsManifest) packageFile(name string) ([]byte, bool) {
	if m.packageFiles == nil || name == "" {
		return nil, false
	}

	contents, ok := m.packageFiles[strings.TrimPrefix(name, "./")]

	return contents, ok
}

// resourceSpecificPermission returns the declared RSC permission with the given
// name, and whether it was found.
func (m *teamsManifest) resourceSpecificPermission(name string) (string, bool) {
	for _, permission := range m.Authorization.Permissions.ResourceSpecific {
		if strings.EqualFold(permission.Name, name) {
			return permission.Type, true
		}
	}

	return "", false
}

// schemaVersion extracts the manifest version a $schema URL pins, for example
// "1.22" from ".../teams/v1.22/MicrosoftTeams.schema.json". It returns an empty
// string when the URL does not carry a recognisable version segment.
func schemaVersion(schemaURL string) string {
	for segment := range strings.SplitSeq(schemaURL, "/") {
		if len(segment) > 1 && segment[0] == 'v' && strings.Contains(segment, ".") {
			return strings.TrimPrefix(segment, "v")
		}
	}

	return ""
}
