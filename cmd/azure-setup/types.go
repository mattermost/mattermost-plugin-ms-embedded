// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/cloudenv"
)

// Microsoft Graph API Permission IDs and Types
const (
	// Graph Resource ID (Microsoft Graph)
	GraphResourceID = "00000003-0000-0000-c000-000000000000"

	// Required Permission IDs from Microsoft Graph
	PermissionUserReadID          = "e1fe6dd8-ba31-4d61-89e7-88639da4683d" // User.Read (Delegated)
	PermissionTeamsActivitySendID = "a267235f-af13-44dc-8385-c1dc93023186" // TeamsActivity.Send (Application)
	PermissionAppCatalogReadAllID = "e12dae10-5a57-4817-b79d-dfbec5348930" // AppCatalog.Read.All (Application)

	// Permission Types
	PermissionTypeScope = "Scope" // Delegated permission
	PermissionTypeRole  = "Role"  // Application permission
)

// Pre-authorized client application IDs for Microsoft first-party applications
// These IDs allow SSO across Teams, Outlook, Office, and other M365 products
// Source: https://learn.microsoft.com/en-us/microsoftteams/platform/tabs/how-to/authentication/tab-sso-register-aad
const (
	// Microsoft Teams
	ClientIDTeamsWeb           = "5e3ce6c0-2b1f-4285-8d4b-75ee78787346"
	ClientIDTeamsMobileDesktop = "1fec8e78-bce4-4aaf-ab1b-5451cc387264" // Covers both mobile and desktop

	// Microsoft Outlook
	ClientIDOutlookWeb     = "bc59ab01-8403-45c6-8796-ac3ef710b3e3"
	ClientIDOutlookDesktop = "d3590ed6-52b3-4102-aeff-aad2292ab01c"
	ClientIDOutlookMobile  = "27922004-5251-4030-b22d-91ecd9a37ea4"

	// Microsoft 365 / Office applications
	ClientIDOfficeWeb     = "4765445b-32c6-49b0-83e6-1d93765276ca"
	ClientIDOfficeDesktop = "0ec893e0-5785-4de6-99da-4ed124e5296c"
	ClientIDOfficeMobile  = "d3590ed6-52b3-4102-aeff-aad2292ab01c" // Shares ID with Outlook Desktop

	// Microsoft 365 Copilot
	// Source: https://learn.microsoft.com/en-us/microsoft-365-copilot/extensibility/api-plugin-authentication
	ClientIDCopilot = "ab3be6b7-f5df-413d-ac2d-abf1e3fd9c0b"

	// Universal client ID that pre-authorizes all Microsoft Office application endpoints
	ClientIDOfficeUniversal = "ea5a67f6-b6f3-4338-b240-c655ddc3cc8e"
)

// doctorRequirementNames are the Microsoft Graph application permissions that
// `azure-setup doctor` needs in order to read an application's configuration,
// its service principal, and the consent grants in the tenant.
//
// They are deliberately NOT part of getRequiredPermissions(): the plugin itself
// never uses them and the doctor audits only the permissions the plugin needs.
// They are added solely by `create --create-doctor-requirements`.
//
// The names are resolved to role IDs at runtime from the Microsoft Graph service
// principal instead of being hardcoded here, so a renamed or re-issued role
// surfaces as a clear error rather than an opaque Graph rejection.
var doctorRequirementNames = []string{
	"Application.Read.All",
	"Directory.Read.All",
}

// Scope configuration
const (
	ScopeName        = "access_as_user"
	ScopeDescription = "Allow the app to access Mattermost on behalf of the signed-in user"
	ScopeUserConsent = "Admins and users"
)

// Plugin configuration
const (
	// PluginID must match the id field in plugin.json
	PluginID = "com.mattermost.ms-embedded"
)

// SetupConfig holds the configuration for the Azure setup process
type SetupConfig struct {
	// User inputs
	TenantID          string
	MattermostSiteURL string
	AppName           string
	ClientID          string // Optional: for updating existing app
	SecretExpiration  int    // Duration in months (default: 12)

	// Flags
	CreateDoctorRequirements bool // Also request the permissions `azure-setup doctor` needs
	DryRun                   bool
	NonInteractive           bool
	Verbose                  bool
	OutputFormat             string // "human", "json", "env"
	SkipConfirmation         bool   // Skip pre-flight confirmation prompt
	Cloud                    string // Microsoft national cloud: "commercial" (default), "gcchigh", or "dod"

	// Internal state
	ctx        context.Context
	credential azcore.TokenCredential
	rollback   []func() error
}

// cloudEnvironment resolves the Microsoft national cloud endpoints for the
// configured Cloud value. It is the single resolution point used across the
// setup flow so every step targets the same, consistent cloud.
func (c *SetupConfig) cloudEnvironment() cloudenv.Environment {
	return cloudenv.EnvironmentFor(c.Cloud)
}

// SetupResult contains the results of the Azure setup operation
type SetupResult struct {
	Success bool
	Message string

	// Azure App Registration Details
	ApplicationClientID string
	TenantID            string
	ClientSecret        string
	SecretExpiration    string

	// App Details
	ApplicationID    string
	ApplicationName  string
	ApplicationIDURI string

	// PortalHost is the Azure portal host for the selected national cloud,
	// used to render the admin-consent link (e.g. portal.azure.com).
	PortalHost string

	// Operation Details
	Created bool // true if created new, false if updated existing
	DryRun  bool

	// DoctorRequirements records that --create-doctor-requirements was used, so
	// the output can point out that admin consent covers those permissions too.
	DoctorRequirements bool
}

// requiredPermission represents a Graph API permission that needs to be configured
type requiredPermission struct {
	ResourceAppID string // The app ID of the resource (e.g., Microsoft Graph)
	ResourceID    string // The permission ID
	Type          string // "Scope" or "Role"
	Name          string // Human-readable name for logging
}

// getRequiredPermissions returns the list of permissions needed for the plugin
func getRequiredPermissions() []requiredPermission {
	return []requiredPermission{
		{
			ResourceAppID: GraphResourceID,
			ResourceID:    PermissionUserReadID,
			Type:          PermissionTypeScope,
			Name:          "User.Read",
		},
		{
			ResourceAppID: GraphResourceID,
			ResourceID:    PermissionTeamsActivitySendID,
			Type:          PermissionTypeRole,
			Name:          "TeamsActivity.Send",
		},
		{
			ResourceAppID: GraphResourceID,
			ResourceID:    PermissionAppCatalogReadAllID,
			Type:          PermissionTypeRole,
			Name:          "AppCatalog.Read.All",
		},
	}
}

// getPreAuthorizedClients returns the list of Microsoft client IDs that should be pre-authorized
// This enables SSO across Teams, Outlook, Office apps, and Copilot on all platforms (web, desktop, mobile)
func getPreAuthorizedClients() []string {
	return []string{
		// Microsoft Teams (web, mobile, desktop)
		ClientIDTeamsWeb,
		ClientIDTeamsMobileDesktop,

		// Microsoft Outlook (web, desktop, mobile)
		ClientIDOutlookWeb,
		ClientIDOutlookDesktop,
		ClientIDOutlookMobile,

		// Microsoft Office / M365 (web, desktop)
		ClientIDOfficeWeb,
		ClientIDOfficeDesktop,
		// Note: Office Mobile shares the same ID as Outlook Desktop, so it's already covered

		// Microsoft 365 Copilot
		ClientIDCopilot,

		// Universal Office endpoints (catch-all for other Office apps)
		ClientIDOfficeUniversal,
	}
}
