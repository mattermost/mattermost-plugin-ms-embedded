// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"

	"github.com/google/uuid"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/pkg/errors"
)

// configureAPIExposure configures the Application ID URI, scopes, and pre-authorized applications
func configureAPIExposure(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig, app models.Applicationable) error {
	if config.Verbose {
		progressln("🌐 Configuring API exposure...")
	}

	// Build the Application ID URI
	appIDURI, err := buildApplicationIDURI(config.MattermostSiteURL, *app.GetAppId())
	if err != nil {
		return errors.Wrap(err, "failed to build Application ID URI")
	}

	if config.DryRun {
		progressln("   [DRY RUN] Would configure API exposure:")
		progressf("      Application ID URI: %s\n", appIDURI)
		progressf("      Scope: %s\n", ScopeName)
		progressln("      Pre-authorized clients:")
		for _, clientID := range getPreAuthorizedClients() {
			progressf("         - %s\n", clientID)
		}
		return nil
	}

	// Reuse the existing scope ID to keep re-runs idempotent; only mint a new
	// UUID when no access_as_user scope exists yet.
	scopeID := findExistingScopeID(app)
	if scopeID == uuid.Nil {
		scopeID = uuid.New()
	}
	scope := models.NewPermissionScope()
	scope.SetId(&scopeID)

	scopeName := ScopeName
	scope.SetValue(&scopeName)

	adminConsentDisplayName := "Access Mattermost"
	scope.SetAdminConsentDisplayName(&adminConsentDisplayName)

	adminConsentDescription := ScopeDescription
	scope.SetAdminConsentDescription(&adminConsentDescription)

	userConsentDisplayName := "Access Mattermost"
	scope.SetUserConsentDisplayName(&userConsentDisplayName)

	userConsentDescription := "Allow the app to access Mattermost on your behalf"
	scope.SetUserConsentDescription(&userConsentDescription)

	scopeType := "User"
	scope.SetTypeEscaped(&scopeType)

	isEnabled := true
	scope.SetIsEnabled(&isEnabled)

	oauth2PermissionScopes := []models.PermissionScopeable{scope}

	// PATCH 1: Set the Application ID URI and the OAuth2 permission scope.
	// Microsoft Graph validates preAuthorizedApplications.delegatedPermissionIds
	// against the application's existing OAuth2PermissionScopes (the pre-PATCH
	// state), so the scope must exist on the application BEFORE we can reference
	// it in preAuthorizedApplications. Doing this in a single PATCH triggers:
	// "Property api.preAuthorizedApplications.delegatedPermissionIds has a
	// Permission Id that cannot be found in the AppPermissions sets."
	identifierUris := []string{appIDURI}
	apiStep1 := models.NewApiApplication()
	apiStep1.SetOauth2PermissionScopes(oauth2PermissionScopes)

	appUpdateStep1 := models.NewApplication()
	appUpdateStep1.SetIdentifierUris(identifierUris)
	appUpdateStep1.SetApi(apiStep1)

	if _, err = client.Applications().ByApplicationId(*app.GetId()).Patch(ctx, appUpdateStep1, nil); err != nil {
		return errors.Wrapf(err, "failed to configure API scope (scope ID: %s, value: %s)", scopeID.String(), ScopeName)
	}

	// PATCH 2: Add pre-authorized applications referencing the scope created above.
	// We re-send oauth2PermissionScopes to preserve it — PATCH semantics on the
	// nested `api` complex type can otherwise wipe the scopes we just set.
	preAuthorizedApps, err := buildPreAuthorizedApplications(scopeID)
	if err != nil {
		return err
	}

	apiStep2 := models.NewApiApplication()
	apiStep2.SetOauth2PermissionScopes(oauth2PermissionScopes)
	apiStep2.SetPreAuthorizedApplications(preAuthorizedApps)

	appUpdateStep2 := models.NewApplication()
	appUpdateStep2.SetApi(apiStep2)

	if _, err = client.Applications().ByApplicationId(*app.GetId()).Patch(ctx, appUpdateStep2, nil); err != nil {
		preAuthClientIDs := getPreAuthorizedClients()
		return errors.Wrapf(err, "failed to configure pre-authorized applications (delegated permission scope ID: %s, value: %s, pre-authorized client IDs: %v)", scopeID.String(), ScopeName, preAuthClientIDs)
	}

	if config.Verbose {
		progressln("✅ API exposure configured:")
		progressf("   ✓ Application ID URI: %s\n", appIDURI)
		progressf("   ✓ Scope: %s\n", ScopeName)
		progressln("   ✓ Pre-authorized clients:")
		progressln("      - Microsoft Teams Web")
		progressln("      - Microsoft Teams Desktop")
		progressln("      - Microsoft Outlook Web")
		progressln("      - Microsoft Outlook Desktop")
	}

	return nil
}

// findExistingScopeID returns the UUID of the existing access_as_user scope on the
// app, or uuid.Nil if none is found. Used to avoid generating a duplicate scope ID
// when configureAPIExposure is called against an already-configured application.
func findExistingScopeID(app models.Applicationable) uuid.UUID {
	if app.GetApi() == nil {
		return uuid.Nil
	}
	for _, scope := range app.GetApi().GetOauth2PermissionScopes() {
		if scope.GetValue() != nil && *scope.GetValue() == ScopeName && scope.GetId() != nil {
			return *scope.GetId()
		}
	}
	return uuid.Nil
}

// buildPreAuthorizedApplications creates the list of pre-authorized applications
// Returns an error if any hardcoded client ID fails to parse (which should never happen)
func buildPreAuthorizedApplications(scopeID uuid.UUID) ([]models.PreAuthorizedApplicationable, error) {
	clientIDs := getPreAuthorizedClients()
	var preAuthorizedApps []models.PreAuthorizedApplicationable

	for _, clientIDStr := range clientIDs {
		clientID, err := uuid.Parse(clientIDStr)
		if err != nil {
			// This should never happen with hardcoded Microsoft client IDs
			// If it does, it indicates a bug in our constants
			return nil, errors.Wrapf(err, "BUG: invalid hardcoded pre-authorized client ID %s", clientIDStr)
		}

		preAuthApp := models.NewPreAuthorizedApplication()
		parsedClientIDStr := clientID.String()
		preAuthApp.SetAppId(&parsedClientIDStr)

		// Add the scope ID to the delegated permission IDs
		delegatedPermissionIDs := []string{scopeID.String()}
		preAuthApp.SetDelegatedPermissionIds(delegatedPermissionIDs)

		preAuthorizedApps = append(preAuthorizedApps, preAuthApp)
	}

	return preAuthorizedApps, nil
}
