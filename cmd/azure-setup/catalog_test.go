// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"
	"testing"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckCatalogPublication(t *testing.T) {
	manifest := loadRealManifest(t)

	t.Run("published and in sync", func(t *testing.T) {
		app := &catalogApp{
			CatalogID:       "catalog-id",
			ExternalID:      manifest.ID,
			DisplayName:     "Mattermost",
			Version:         manifest.Version,
			PublishingState: "published",
		}

		result := checkCatalogPublication(manifest, app, nil)
		assert.Equal(t, StatusPass, result.Status)
		assert.Contains(t, result.Summary, "Mattermost")
	})

	t.Run("not published fails", func(t *testing.T) {
		result := checkCatalogPublication(manifest, nil, nil)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, manifest.ID)
		assert.Contains(t, result.Remediation, "Teams admin center")
	})

	t.Run("stale version warns", func(t *testing.T) {
		app := &catalogApp{
			CatalogID:       "catalog-id",
			DisplayName:     "Mattermost",
			Version:         "1.0.7",
			PublishingState: "published",
		}

		result := checkCatalogPublication(manifest, app, nil)
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "1.0.7")
		assert.Contains(t, result.Summary, manifest.Version)
	})

	t.Run("unpublished state warns", func(t *testing.T) {
		app := &catalogApp{
			CatalogID:       "catalog-id",
			DisplayName:     "Mattermost",
			Version:         manifest.Version,
			PublishingState: "submitted",
		}

		result := checkCatalogPublication(manifest, app, nil)
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "submitted")
	})

	t.Run("unreadable catalog skips", func(t *testing.T) {
		result := checkCatalogPublication(manifest, nil, assert.AnError)
		assert.Equal(t, StatusSkip, result.Status)
		assert.Contains(t, result.Remediation, "AppCatalog.Read.All")
	})

	t.Run("store distribution is reported, not treated as missing", func(t *testing.T) {
		// Graph leaves externalId empty for store-distributed apps and matches
		// the manifest id against the catalog id instead.
		app := &catalogApp{
			CatalogID:          manifest.ID,
			ExternalID:         "",
			DisplayName:        "Mattermost",
			DistributionMethod: "store",
			Version:            manifest.Version,
			PublishingState:    "published",
			MatchedByCatalogID: true,
		}

		result := checkCatalogPublication(manifest, app, nil)
		assert.Equal(t, StatusPass, result.Status)
		assert.Contains(t, strings.Join(result.Details, "\n"), "store-distributed")
	})
}

func TestDescribeCatalogApp(t *testing.T) {
	definitionOld := models.NewTeamsAppDefinition()
	definitionOld.SetVersion(ptr("1.0.7"))

	definitionNew := models.NewTeamsAppDefinition()
	definitionNew.SetVersion(ptr("1.0.8"))
	state, err := models.ParseTeamsAppPublishingState("published")
	require.NoError(t, err)
	definitionNew.SetPublishingState(state.(*models.TeamsAppPublishingState))

	app := models.NewTeamsApp()
	app.SetId(ptr("catalog-id"))
	app.SetExternalId(ptr(realManifestAppID))
	app.SetDisplayName(ptr("Mattermost"))
	app.SetAppDefinitions([]models.TeamsAppDefinitionable{definitionOld, definitionNew})

	described := describeCatalogApp(app)

	assert.Equal(t, "catalog-id", described.CatalogID)
	assert.Equal(t, realManifestAppID, described.ExternalID)
	assert.Equal(t, "Mattermost", described.DisplayName)
	// The newest definition is the one installed in the tenant.
	assert.Equal(t, "1.0.8", described.Version)
	assert.Equal(t, "published", described.PublishingState)
}

func TestNewestDefinitionDoesNotAssumeOrdering(t *testing.T) {
	// Graph documents no ordering for appDefinitions, so the newest must be
	// chosen by version rather than by position.
	definition := func(version string) models.TeamsAppDefinitionable {
		d := models.NewTeamsAppDefinition()
		d.SetVersion(ptr(version))

		return d
	}

	app := models.NewTeamsApp()
	app.SetAppDefinitions([]models.TeamsAppDefinitionable{
		definition("1.0.10"),
		definition("1.0.9"),
		definition("1.0.2"),
	})

	assert.Equal(t, "1.0.10", describeCatalogApp(app).Version,
		"1.0.10 is newer than 1.0.9 despite sorting lower lexically")
}

func TestCompareVersions(t *testing.T) {
	assert.Negative(t, compareVersions("1.0.2", "1.0.10"))
	assert.Positive(t, compareVersions("1.1.0", "1.0.99"))
	assert.Zero(t, compareVersions("1.0.8", "1.0.8"))
	assert.Positive(t, compareVersions("1.0.1", "1.0"))
	// Non-numeric parts still order deterministically rather than panicking.
	assert.NotPanics(t, func() { compareVersions("1.0.0-rc1", "1.0.0") })
}
