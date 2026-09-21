// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/cloudenv"
)

// validateInputs validates all user inputs before proceeding
func validateInputs(config *SetupConfig) error {
	if err := validateSiteURL(config.MattermostSiteURL); err != nil {
		return err
	}

	// Validate app name
	if config.AppName == "" {
		return errors.New("application name is required")
	}

	if len(config.AppName) < 3 {
		return errors.New("application name must be at least 3 characters")
	}

	// Validate secret expiration
	if config.SecretExpiration <= 0 {
		config.SecretExpiration = 12 // Default to 12 months
	}

	if config.SecretExpiration > 24 {
		return errors.New("secret expiration cannot exceed 24 months")
	}

	// Validate and normalize the national cloud.
	env, err := resolveCloud(config.Cloud)
	if err != nil {
		return err
	}
	config.Cloud = env.Name

	return nil
}

// validateSiteURL checks that a Mattermost site URL is usable as the basis of
// the Application ID URI: url.Parse accepts almost any non-empty string, so the
// scheme and host are verified explicitly.
func validateSiteURL(siteURL string) error {
	if siteURL == "" {
		return errors.New("Mattermost site URL is required")
	}

	u, err := url.Parse(siteURL)
	if err != nil {
		return errors.Wrap(err, "invalid Mattermost site URL")
	}

	if u.Scheme == "" || u.Host == "" {
		return errors.New("Mattermost site URL must include protocol (https://) and hostname")
	}

	if u.Scheme != "https" {
		return errors.New("Mattermost site URL must use HTTPS protocol")
	}

	return nil
}

// resolveCloud normalizes and validates a national cloud name supplied via the
// --cloud flag and returns the resolved environment. Normalization (trim and
// lowercase) matches cloudenv.EnvironmentFor so validation and resolution agree,
// an empty value defaults to the commercial cloud, and any value not in
// cloudenv.Names() is rejected so typos fail fast instead of silently defaulting.
func resolveCloud(name string) (cloudenv.Environment, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		normalized = cloudenv.Commercial
	}
	if !slices.Contains(cloudenv.Names(), normalized) {
		return cloudenv.Environment{}, errors.Errorf("invalid --cloud value %q: must be one of %v", name, cloudenv.Names())
	}
	return cloudenv.EnvironmentFor(normalized), nil
}

// directoryContext describes the signed-in identity and the application
// administration roles it holds.
type directoryContext struct {
	UserPrincipalName string

	// AdminRoles lists the display names of the application administration roles
	// held by the signed-in user.
	AdminRoles []string

	// RolesErr records a failure to read the directory role membership. Role
	// lookup is not always permitted, so callers treat this as unknown rather
	// than as a lack of permissions.
	RolesErr error
}

// describeSignedInUser resolves who is signed in and which application
// administration roles they hold.
func describeSignedInUser(ctx context.Context, client *msgraphsdk.GraphServiceClient) (directoryContext, error) {
	var directory directoryContext

	me, err := client.Me().Get(ctx, nil)
	if err != nil {
		return directory, errors.Wrap(err, "failed to get current user information")
	}

	if me == nil {
		return directory, errors.New("Azure returned no information for the current user")
	}
	directory.UserPrincipalName = derefString(me.GetUserPrincipalName())

	memberOf, err := client.Me().MemberOf().Get(ctx, nil)
	if err != nil {
		directory.RolesErr = err
		return directory, nil
	}

	if memberOf == nil {
		return directory, nil
	}

	// memberOf returns groups as well as directory roles, so a user in a
	// well-populated tenant can easily span several pages and have their
	// administrator role land on a later one. Reading only the first page
	// would report a Global Administrator as holding no admin role.
	if err = iteratePages(ctx, client, memberOf,
		models.CreateDirectoryObjectCollectionResponseFromDiscriminatorValue,
		func(member models.DirectoryObjectable) {
			directoryRole, ok := member.(models.DirectoryRoleable)
			if !ok || directoryRole.GetRoleTemplateId() == nil {
				return
			}

			if isApplicationAdminRole(*directoryRole.GetRoleTemplateId()) {
				directory.AdminRoles = append(directory.AdminRoles, derefString(directoryRole.GetDisplayName()))
			}
		}); err != nil {
		directory.RolesErr = err
	}

	return directory, nil
}

// validatePermissions checks if the authenticated user has the necessary permissions
// to create and manage applications: an Application Administrator, Cloud Application
// Administrator, or Global Administrator role, or a tenant that lets users create
// applications. Missing roles are reported as a warning so the operation can still
// proceed and fail on the actual Graph call if permissions turn out to be too narrow.
func validatePermissions(ctx context.Context, client *msgraphsdk.GraphServiceClient, verbose bool) error {
	if verbose {
		progressln("🔍 Checking user permissions...")
	}

	directory, err := describeSignedInUser(ctx, client)
	if err != nil {
		return err
	}

	if verbose {
		progressf("   Authenticated as: %s\n", directory.UserPrincipalName)
	}

	if directory.RolesErr != nil {
		// Always show this warning as it's important for users to know
		progressln("⚠️  Warning: Could not check directory roles")
		if verbose {
			progressf("   Error details: %v\n", directory.RolesErr)
		}
		return nil
	}

	if len(directory.AdminRoles) == 0 {
		// Always show this warning as it's critical for users to know
		progressln("⚠️  Warning: User may not have Application Administrator permissions")
		progressln("   Setup will proceed, but may fail if permissions are insufficient")
		progressln("   Required role: Application Administrator, Cloud Application Administrator, or Global Administrator")
		return nil
	}

	if verbose {
		progressf("   ✅ User has admin role: %s\n", strings.Join(directory.AdminRoles, ", "))
		progressln("✅ User has sufficient permissions")
	}

	return nil
}

// isApplicationAdminRole checks if a role template ID corresponds to an application admin role
func isApplicationAdminRole(roleTemplateID string) bool {
	// Known Azure AD role template IDs for application management
	adminRoles := map[string]bool{
		"9b895d92-2cd3-44c7-9d02-a6ac2d5ea5c3": true, // Application Administrator
		"158c047a-c907-4556-b7ef-446551a6b5f7": true, // Cloud Application Administrator
		"62e90394-69f5-4237-9190-012177145e10": true, // Global Administrator
	}
	return adminRoles[roleTemplateID]
}

// escapeODataString escapes a string for use in OData filter expressions
// OData escaping rules:
// - Each single quote (apostrophe) is escaped by writing it twice
// Note: Other special characters are generally handled by the Graph SDK
func escapeODataString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// checkExistingApp checks if an application with the given name or client ID already exists
func checkExistingApp(ctx context.Context, client *msgraphsdk.GraphServiceClient, appName, clientID string, verbose bool) (models.Applicationable, error) {
	if verbose {
		progressln("🔍 Checking for existing application...")
	}

	var filter string
	if clientID != "" {
		// Search by client ID (appId field)
		// Escape for OData filter - client IDs are UUIDs so escaping is unlikely needed
		// but we do it for consistency
		escapedClientID := escapeODataString(clientID)
		filter = fmt.Sprintf("appId eq '%s'", escapedClientID)
	} else {
		// Search by display name
		// Escape for OData filter
		escapedAppName := escapeODataString(appName)
		filter = fmt.Sprintf("displayName eq '%s'", escapedAppName)
	}

	apps, err := client.Applications().Get(ctx, &applications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &applications.ApplicationsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: []string{"id", "appId", "displayName", "api", "identifierUris", "signInAudience", "requiredResourceAccess"},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to search for existing application")
	}

	if apps == nil || apps.GetValue() == nil || len(apps.GetValue()) == 0 {
		if verbose {
			progressln("   No existing application found")
		}
		return nil, nil
	}

	existingApp := apps.GetValue()[0]
	if verbose {
		progressf("   ✅ Found existing application: %s (ID: %s)\n",
			*existingApp.GetDisplayName(),
			*existingApp.GetAppId())
	}

	return existingApp, nil
}

// extractHostnameAndPath extracts the hostname and path from a Mattermost URL
// Returns hostname (without protocol) and path
func extractHostnameAndPath(siteURL string) (string, string, error) {
	u, err := url.Parse(siteURL)
	if err != nil {
		return "", "", err
	}

	hostname := u.Host
	path := strings.TrimSuffix(u.Path, "/")

	return hostname, path, nil
}

// buildApplicationIDURI builds the Application ID URI in the format: api://hostname/path/clientID
func buildApplicationIDURI(siteURL, clientID string) (string, error) {
	hostname, path, err := extractHostnameAndPath(siteURL)
	if err != nil {
		return "", err
	}

	// Build the URI
	if path == "" || path == "/" {
		return fmt.Sprintf("api://%s/%s", hostname, clientID), nil
	}

	return fmt.Sprintf("api://%s%s/%s", hostname, path, clientID), nil
}

// getTenantID retrieves the tenant ID from the Azure organization
func getTenantID(ctx context.Context, client *msgraphsdk.GraphServiceClient, verbose bool) (string, error) {
	if verbose {
		progressln("🔍 Retrieving tenant ID from Azure...")
	}

	// Get organization details to retrieve tenant ID
	org, err := client.Organization().Get(ctx, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to get organization information")
	}

	if org == nil || org.GetValue() == nil || len(org.GetValue()) == 0 {
		return "", errors.New("no organization information found")
	}

	orgInfo := org.GetValue()[0]
	tenantID := orgInfo.GetId()
	if tenantID == nil || *tenantID == "" {
		return "", errors.New("organization ID is empty")
	}

	if verbose {
		progressf("   ✅ Tenant ID: %s\n", *tenantID)
	}

	return *tenantID, nil
}

// adminConsentURL builds the Azure portal deep link an administrator uses to
// grant admin consent for the application's API permissions.
func adminConsentURL(portalHost, clientID string) string {
	if portalHost == "" {
		portalHost = "portal.azure.com"
	}

	return fmt.Sprintf("https://%s/#view/Microsoft_AAD_RegisteredApps/ApplicationMenuBlade/~/CallAnAPI/appId/%s", portalHost, clientID)
}
