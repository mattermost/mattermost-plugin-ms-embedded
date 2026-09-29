// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/appcatalogs"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

// catalogApp is the Teams app published to the tenant, reduced to what the
// report needs.
type catalogApp struct {
	CatalogID          string
	ExternalID         string
	DisplayName        string
	DistributionMethod string

	// Version and PublishingState come from the newest app definition.
	Version         string
	PublishingState string

	// MatchedByCatalogID records that the app was found by its catalog id
	// rather than its externalId, which is how store-distributed apps appear.
	MatchedByCatalogID bool
}

// findCatalogApp locates the Teams app for a manifest id in the tenant's app
// catalog, the same lookup the plugin performs before sending a notification.
// Store-distributed apps have an empty externalId and match on the catalog id instead.
func findCatalogApp(ctx context.Context, client *msgraphsdk.GraphServiceClient, manifestID string) (*catalogApp, error) {
	app, err := queryCatalog(ctx, client, fmt.Sprintf("externalId eq '%s'", escapeODataString(manifestID)))
	if err != nil {
		return nil, err
	}

	if app != nil {
		return app, nil
	}

	app, err = queryCatalog(ctx, client, fmt.Sprintf("id eq '%s'", escapeODataString(manifestID)))
	if err != nil {
		return nil, err
	}

	if app != nil {
		app.MatchedByCatalogID = true
	}

	return app, nil
}

func queryCatalog(ctx context.Context, client *msgraphsdk.GraphServiceClient, filter string) (*catalogApp, error) {
	expand := []string{"appDefinitions"}

	response, err := client.AppCatalogs().TeamsApps().Get(ctx, &appcatalogs.TeamsAppsRequestBuilderGetRequestConfiguration{
		QueryParameters: &appcatalogs.TeamsAppsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Expand: expand,
		},
	})
	if err != nil {
		return nil, err
	}

	if response == nil || len(response.GetValue()) == 0 {
		return nil, nil
	}

	return describeCatalogApp(response.GetValue()[0]), nil
}

func describeCatalogApp(app models.TeamsAppable) *catalogApp {
	described := &catalogApp{
		CatalogID:   derefString(app.GetId()),
		ExternalID:  derefString(app.GetExternalId()),
		DisplayName: derefString(app.GetDisplayName()),
	}

	if method := app.GetDistributionMethod(); method != nil {
		described.DistributionMethod = method.String()
	}

	// Graph documents no ordering for appDefinitions.
	if newest := newestDefinition(app.GetAppDefinitions()); newest != nil {
		described.Version = derefString(newest.GetVersion())

		if state := newest.GetPublishingState(); state != nil {
			described.PublishingState = state.String()
		}
	}

	return described
}

// newestDefinition returns the highest-versioned app definition.
func newestDefinition(definitions []models.TeamsAppDefinitionable) models.TeamsAppDefinitionable {
	var newest models.TeamsAppDefinitionable

	for _, definition := range definitions {
		if newest == nil || compareVersions(derefString(definition.GetVersion()), derefString(newest.GetVersion())) > 0 {
			newest = definition
		}
	}

	return newest
}

// compareVersions orders two dotted version strings numerically where it can,
// falling back to a lexical comparison for anything non-numeric so that
// pre-release suffixes still order deterministically.
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		var aPart, bPart string
		if i < len(aParts) {
			aPart = aParts[i]
		}
		if i < len(bParts) {
			bPart = bParts[i]
		}

		aNum, aErr := strconv.Atoi(aPart)
		bNum, bErr := strconv.Atoi(bPart)

		if aErr == nil && bErr == nil {
			if aNum != bNum {
				return cmp.Compare(aNum, bNum)
			}

			continue
		}

		if aPart != bPart {
			return cmp.Compare(aPart, bPart)
		}
	}

	return 0
}

// checkCatalogPublication reports whether the manifest has actually been
// uploaded to the tenant, and whether what is published matches the file.
func checkCatalogPublication(manifest *teamsManifest, app *catalogApp, lookupErr error) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "Published to the tenant"}

	if lookupErr != nil {
		result.Status = StatusSkip
		result.Summary = "Could not read the Teams app catalog: " + lookupErr.Error()
		result.Remediation = "Grant the credential AppCatalog.Read.All to verify the app is installed"

		return result
	}

	if app == nil {
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("No Teams app with id %s is published in this tenant, so notifications cannot be delivered", manifest.ID)
		result.Details = append(result.Details,
			"the plugin resolves this id before sending an activity notification")
		result.Remediation = "Upload the app package in the Microsoft Teams admin center"

		return result
	}

	result.Details = append(result.Details, "catalog id: "+app.CatalogID)
	if app.DistributionMethod != "" {
		result.Details = append(result.Details, "distribution: "+app.DistributionMethod)
	}
	if app.MatchedByCatalogID {
		result.Details = append(result.Details,
			"matched by catalog id; externalId is empty for store-distributed apps")
	}

	var problems []string

	if app.Version != "" {
		result.Details = append(result.Details, "published version: "+app.Version)

		if manifest.Version != "" && app.Version != manifest.Version {
			problems = append(problems, fmt.Sprintf("the tenant has version %s but this file is %s",
				app.Version, manifest.Version))
		}
	}

	if app.PublishingState != "" && !strings.EqualFold(app.PublishingState, "published") {
		problems = append(problems, "publishing state is "+app.PublishingState)
	}

	if len(problems) > 0 {
		result.Status = StatusWarn
		result.Summary = strings.Join(problems, "; ")
		result.Remediation = "Upload the current app package in the Microsoft Teams admin center"

		return result
	}

	result.Status = StatusPass
	result.Summary = "Published as " + app.DisplayName

	return result
}
