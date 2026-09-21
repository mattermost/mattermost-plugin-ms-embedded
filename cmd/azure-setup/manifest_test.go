// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realManifestPath is a copy of a genuine published manifest, used in
// preference to an invented fixture so the tests track the real shape.
//
// It lives in testdata rather than being read out of appstore/ because not
// every directory there is tracked, and a test must not depend on a file that
// is absent from a fresh clone.
const realManifestPath = "testdata/manifest.json"

const (
	realManifestAppID    = "97e1dd8c-aa8e-4253-9bff-6b70dbf0d7e3"
	realManifestClientID = "4ee1a558-558d-497a-b64a-bddd771292d6"
	realManifestHost     = "community.mattermost.com"
)

// realManifestResource is the identifier URI the published manifest declares.
var realManifestResource = "api://" + realManifestHost + "/" + realManifestClientID

// loadRealManifest parses the published manifest committed to the repo.
func loadRealManifest(t *testing.T) *teamsManifest {
	t.Helper()

	manifest, err := loadManifest(realManifestPath)
	require.NoError(t, err)

	return manifest
}

// rawRealManifest returns the published manifest decoded into a generic map so
// tests can mutate one field and re-encode.
func rawRealManifest(t *testing.T) map[string]any {
	t.Helper()

	data, err := os.ReadFile(realManifestPath)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	return raw
}

// writeManifestFile writes a mutated manifest to a temp file and loads it.
func writeManifestFile(t *testing.T, raw map[string]any) *teamsManifest {
	t.Helper()

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), ManifestName)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))

	manifest, err := loadManifest(path)
	require.NoError(t, err)

	return manifest
}

// pngOfSize builds a minimal PNG whose IHDR declares the given dimensions.
// Only the signature and IHDR are needed; nothing decodes the image data.
func pngOfSize(width, height int) []byte {
	var buf bytes.Buffer

	buf.WriteString("\x89PNG\r\n\x1a\n")

	ihdr := make([]byte, 0, 25)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 13)
	ihdr = append(ihdr, []byte("IHDR")...)
	// #nosec G115 -- dimensions are small constants chosen by the tests
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(width))
	// #nosec G115 -- as above
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(height))
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	ihdr = binary.BigEndian.AppendUint32(ihdr, crc32.ChecksumIEEE(ihdr[4:]))

	buf.Write(ihdr)

	return buf.Bytes()
}

// buildPackage zips the given entries into a temp file and returns its path.
func buildPackage(t *testing.T, entries map[string][]byte) string {
	t.Helper()

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	for name, contents := range entries {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write(contents)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	path := filepath.Join(t.TempDir(), "app.zip")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))

	return path
}

// realPackage builds an app package around the published manifest.
func realPackage(t *testing.T, mutate func(raw map[string]any), icons map[string][]byte) string {
	t.Helper()

	raw := rawRealManifest(t)
	if mutate != nil {
		mutate(raw)
	}

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	entries := map[string][]byte{ManifestName: encoded}
	maps.Copy(entries, icons)

	return buildPackage(t, entries)
}

func defaultIcons() map[string][]byte {
	return map[string][]byte{
		"icon-color.png":   pngOfSize(IconColorSize, IconColorSize),
		"icon-outline.png": pngOfSize(IconOutlineSize, IconOutlineSize),
	}
}

func TestLoadManifestFromJSON(t *testing.T) {
	manifest := loadRealManifest(t)

	assert.Equal(t, realManifestAppID, manifest.ID)
	assert.Equal(t, realManifestClientID, manifest.WebApplicationInfo.ID)
	assert.Equal(t, realManifestResource, manifest.WebApplicationInfo.Resource)
	assert.Equal(t, []string{realManifestHost}, manifest.ValidDomains)
	assert.Equal(t, "1.22", manifest.ManifestVersion)
	assert.False(t, manifest.FromPackage)

	// The published manifest proves these two are independent identifiers.
	assert.NotEqual(t, manifest.ID, manifest.WebApplicationInfo.ID)
}

func TestLoadManifestFromPackage(t *testing.T) {
	path := realPackage(t, nil, defaultIcons())

	manifest, err := loadManifest(path)
	require.NoError(t, err)

	assert.True(t, manifest.FromPackage)
	assert.Equal(t, realManifestAppID, manifest.ID)

	// Detection is by content, not extension.
	renamed := filepath.Join(t.TempDir(), "package.bin")
	data, err := os.ReadFile(path) // #nosec G304 -- path is from t.TempDir()
	require.NoError(t, err)
	// #nosec G703 -- renamed is built from t.TempDir()
	require.NoError(t, os.WriteFile(renamed, data, 0o600))

	fromRenamed, err := loadManifest(renamed)
	require.NoError(t, err)
	assert.True(t, fromRenamed.FromPackage)
}

func TestLoadManifestErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := loadManifest(filepath.Join(t.TempDir(), "absent.json"))
		require.Error(t, err)
	})

	t.Run("not JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ManifestName)
		require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))

		_, err := loadManifest(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Teams app manifest")
	})

	t.Run("package without a manifest", func(t *testing.T) {
		path := buildPackage(t, map[string][]byte{"icon-color.png": pngOfSize(192, 192)})

		_, err := loadManifest(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "contains no manifest.json")
	})
}

func TestSchemaVersion(t *testing.T) {
	assert.Equal(t, "1.22", schemaVersion("https://developer.microsoft.com/en-us/json-schemas/teams/v1.22/MicrosoftTeams.schema.json"))
	assert.Equal(t, "1.30", schemaVersion("https://example.com/teams/v1.30/x.json"))
	assert.Empty(t, schemaVersion("https://example.com/teams/schema.json"))
	assert.Empty(t, schemaVersion(""))
}

func TestPNGDimensions(t *testing.T) {
	width, height, ok := pngDimensions(pngOfSize(192, 32))
	require.True(t, ok)
	assert.Equal(t, 192, width)
	assert.Equal(t, 32, height)

	_, _, ok = pngDimensions([]byte("not a png"))
	assert.False(t, ok)

	_, _, ok = pngDimensions(nil)
	assert.False(t, ok)
}

func TestLoadManifestRejectsAnOversizedPackage(t *testing.T) {
	// Each entry was capped individually, which still let many entries add up
	// without bound. The limit that matters is the total.
	entries := map[string][]byte{ManifestName: []byte(`{"id":"x"}`)}

	chunk := bytes.Repeat([]byte("A"), 1<<20)
	for i := range 12 {
		entries[fmt.Sprintf("filler-%d.bin", i)] = chunk
	}

	_, err := loadManifest(buildPackage(t, entries))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "beyond the")
}

func TestLoadManifestAcceptsANormalPackage(t *testing.T) {
	// The cumulative limit must not reject a realistic package.
	manifest, err := loadManifest(realPackage(t, nil, defaultIcons()))
	require.NoError(t, err)
	assert.True(t, manifest.FromPackage)
}
