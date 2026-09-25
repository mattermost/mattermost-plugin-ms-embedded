// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/pkg/errors"
)

// createOrUpdateApp creates a new application or updates an existing one
func createOrUpdateApp(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig, existingApp models.Applicationable) (models.Applicationable, bool, error) {
	if existingApp != nil {
		if config.Verbose {
			progressln("📝 Updating existing application...")
		}
		app, err := updateApplication(ctx, client, config, existingApp)
		return app, false, err
	}

	if config.Verbose {
		progressln("🆕 Creating new application...")
	}
	app, err := createApplication(ctx, client, config)
	return app, true, err
}

// createApplication creates a new Azure AD application registration
func createApplication(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig) (models.Applicationable, error) {
	if config.DryRun {
		progressln("   [DRY RUN] Would create new application:", config.AppName)
		// Return a mock app for dry run
		mockApp := models.NewApplication()
		displayName := config.AppName
		mockApp.SetDisplayName(&displayName)
		appID := "00000000-0000-0000-0000-000000000000"
		mockApp.SetAppId(&appID)
		objectID := "00000000-0000-0000-0000-000000000000"
		mockApp.SetId(&objectID)
		return mockApp, nil
	}

	// Create the application object
	app := models.NewApplication()
	app.SetDisplayName(&config.AppName)

	// Set sign-in audience to single tenant
	signInAudience := "AzureADMyOrg"
	app.SetSignInAudience(&signInAudience)

	// Create the application
	createdApp, err := client.Applications().Post(ctx, app, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create application")
	}

	if config.Verbose {
		progressf("✅ Application created: %s\n", *createdApp.GetDisplayName())
		progressf("   Client ID: %s\n", *createdApp.GetAppId())
		progressf("   Object ID: %s\n", *createdApp.GetId())
	}

	// Add to rollback list
	config.rollback = append(config.rollback, func() error {
		return deleteApplication(ctx, client, *createdApp.GetId(), config.Verbose)
	})

	return createdApp, nil
}

// updateApplication updates an existing Azure AD application registration
func updateApplication(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig, existingApp models.Applicationable) (models.Applicationable, error) {
	if config.DryRun {
		progressln("   [DRY RUN] Would update existing application:", *existingApp.GetDisplayName())
		return existingApp, nil
	}

	// Check if the application needs updates for idempotency
	needsUpdate := false
	appUpdate := models.NewApplication()

	// Check sign-in audience
	expectedAudience := "AzureADMyOrg"
	if existingApp.GetSignInAudience() == nil || *existingApp.GetSignInAudience() != expectedAudience {
		appUpdate.SetSignInAudience(&expectedAudience)
		needsUpdate = true
		if config.Verbose {
			progressf("   ⚙️  Will update sign-in audience to: %s\n", expectedAudience)
		}
	}

	// If no updates needed, return the existing app as-is
	if !needsUpdate {
		if config.Verbose {
			progressf("✅ Existing application is already configured correctly: %s\n", *existingApp.GetDisplayName())
			progressf("   Client ID: %s\n", *existingApp.GetAppId())
			progressf("   Object ID: %s\n", *existingApp.GetId())
		}
		return existingApp, nil
	}

	// Apply updates
	if config.Verbose {
		progressf("📝 Updating application configuration: %s\n", *existingApp.GetDisplayName())
	}

	updatedApp, err := client.Applications().ByApplicationId(*existingApp.GetId()).Patch(ctx, appUpdate, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to update application")
	}

	// Microsoft Graph answers a successful PATCH with 204 No Content, which the
	// SDK surfaces as a nil application. The patch did apply, so fall back to
	// the object already in hand: later steps dereference its ID and read its
	// existing requiredResourceAccess to avoid dropping permissions.
	if updatedApp == nil {
		updatedApp = existingApp
	}

	if config.Verbose {
		progressf("✅ Application updated: %s\n", derefString(updatedApp.GetDisplayName()))
		progressf("   Client ID: %s\n", derefString(updatedApp.GetAppId()))
		progressf("   Object ID: %s\n", derefString(updatedApp.GetId()))
	}

	return updatedApp, nil
}

// deleteApplication deletes an application (used for rollback)
func deleteApplication(ctx context.Context, client *msgraphsdk.GraphServiceClient, objectID string, verbose bool) error {
	if verbose {
		progressf("🗑️  Rolling back: Deleting application %s\n", objectID)
	}

	err := client.Applications().ByApplicationId(objectID).Delete(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to delete application during rollback")
	}

	return nil
}
