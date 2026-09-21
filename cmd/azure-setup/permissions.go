// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
	"github.com/pkg/errors"
)

// resolvePermissions returns every Graph permission this run will request: the
// ones the plugin needs, plus the read-only ones the doctor needs when
// --create-doctor-requirements was given.
//
// It is called before the pre-flight confirmation rather than inside
// configureAPIPermissions so that the list the operator approves is the same
// list that gets requested. The doctor permissions are the two the README
// warns about - they grant the plugin's own client secret tenant-wide
// directory read - so they are the last thing a consent prompt should omit.
func resolvePermissions(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig) ([]requiredPermission, error) {
	permissions := getRequiredPermissions()

	if !config.CreateDoctorRequirements {
		return permissions, nil
	}

	doctorPermissions, err := resolveGraphAppRoles(ctx, client, doctorRequirementNames)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve the permissions required by the doctor command")
	}

	return append(permissions, doctorPermissions...), nil
}

// configureAPIPermissions requests the given permissions on the application,
// preserving any it does not manage, and ensures the service principal exists
// so that admin consent can be granted.
func configureAPIPermissions(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig, app models.Applicationable, permissions []requiredPermission) error {
	if config.Verbose {
		progressln("🔑 Configuring API permissions...")
	}

	if config.DryRun {
		progressln("   [DRY RUN] Would configure the following API permissions:")
		for _, perm := range permissions {
			progressf("      - %s (%s)\n", perm.Name, permissionKindLabel(perm.Type))
		}
		return nil
	}

	// Build the required resource access list
	requiredResourceAccess, err := buildRequiredResourceAccess(permissions)
	if err != nil {
		return err
	}

	requiredResourceAccess = mergeExistingResourceAccess(requiredResourceAccess, app)

	// Update the application with the required permissions
	appUpdate := models.NewApplication()
	appUpdate.SetRequiredResourceAccess(requiredResourceAccess)

	_, err = client.Applications().ByApplicationId(*app.GetId()).Patch(ctx, appUpdate, nil)
	if err != nil {
		return errors.Wrap(err, "failed to configure API permissions")
	}

	if config.Verbose {
		progressln("✅ API permissions configured:")
		for _, perm := range permissions {
			progressf("   ✓ %s (%s)\n", perm.Name, permissionKindLabel(perm.Type))
		}
	}

	// Create service principal to enable admin consent
	if err := ensureServicePrincipalExists(ctx, client, config, app); err != nil {
		progressf("\n⚠️  WARNING: Could not create service principal: %v\n", err)
		progressln("   The service principal is required for admin consent to work properly")
		progressln("   You may need to create it manually in the Azure Portal")
		if config.Verbose {
			progressf("   Error details: %v\n", err)
		}
	}

	return nil
}

// resolveGraphAppRoles looks up Microsoft Graph application permissions by name
// on the Graph service principal and returns them as requiredPermission entries.
// Resolving at runtime keeps the role IDs out of the source: a name Graph does
// not recognize fails with a readable error instead of a rejected PATCH.
func resolveGraphAppRoles(ctx context.Context, client *msgraphsdk.GraphServiceClient, names []string) ([]requiredPermission, error) {
	graphSP, err := findGraphServicePrincipal(ctx, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read the Microsoft Graph service principal")
	}

	available := make(map[string]string)
	for _, role := range graphSP.GetAppRoles() {
		if role.GetValue() != nil && role.GetId() != nil {
			available[*role.GetValue()] = role.GetId().String()
		}
	}

	permissions := make([]requiredPermission, 0, len(names))
	for _, name := range names {
		roleID, ok := available[name]
		if !ok {
			return nil, errors.Errorf("Microsoft Graph does not expose an application permission named %q in this tenant", name)
		}

		permissions = append(permissions, requiredPermission{
			ResourceAppID: GraphResourceID,
			ResourceID:    roleID,
			Type:          PermissionTypeRole,
			Name:          name,
		})
	}

	return permissions, nil
}

// buildRequiredResourceAccess builds the required resource access list for the
// given permissions, grouped by the resource they belong to.
func buildRequiredResourceAccess(permissions []requiredPermission) ([]models.RequiredResourceAccessable, error) {
	// Group permissions by resource app ID
	resourceMap := make(map[string][]models.ResourceAccessable)

	for _, perm := range permissions {
		permUUID, err := uuid.Parse(perm.ResourceID)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid permission ID: %s", perm.ResourceID)
		}

		resourceAccess := models.NewResourceAccess()
		resourceAccess.SetId(&permUUID)
		resourceAccess.SetTypeEscaped(&perm.Type)

		resourceMap[perm.ResourceAppID] = append(resourceMap[perm.ResourceAppID], resourceAccess)
	}

	// Build the RequiredResourceAccess list
	var requiredResourceAccess []models.RequiredResourceAccessable

	for resourceAppID, accesses := range resourceMap {
		resourceUUID, err := uuid.Parse(resourceAppID)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid resource app ID: %s", resourceAppID)
		}

		required := models.NewRequiredResourceAccess()
		resourceAppIDStr := resourceUUID.String()
		required.SetResourceAppId(&resourceAppIDStr)
		required.SetResourceAccess(accesses)

		requiredResourceAccess = append(requiredResourceAccess, required)
	}

	return requiredResourceAccess, nil
}

// mergeExistingResourceAccess carries over any permission already on the
// application that is not in the desired set.
//
// A PATCH replaces requiredResourceAccess wholesale, so without this a re-run
// would silently strip permissions this invocation does not know about: the
// doctor permissions added by an earlier --create-doctor-requirements run, or
// anything an administrator added by hand.
func mergeExistingResourceAccess(desired []models.RequiredResourceAccessable, app models.Applicationable) []models.RequiredResourceAccessable {
	if app == nil || len(app.GetRequiredResourceAccess()) == 0 {
		return desired
	}

	// The desired entries are copied rather than extended in place: callers
	// build them from a shared permission list and must not see them grow.
	groups := make(map[string]models.RequiredResourceAccessable, len(desired))
	present := make(map[string]bool)
	merged := make([]models.RequiredResourceAccessable, 0, len(desired)+1)

	for _, resource := range desired {
		resourceAppID := derefString(resource.GetResourceAppId())
		key := strings.ToLower(resourceAppID)

		group := models.NewRequiredResourceAccess()
		group.SetResourceAppId(&resourceAppID)
		group.SetResourceAccess(slices.Clone(resource.GetResourceAccess()))

		groups[key] = group
		merged = append(merged, group)

		for _, access := range resource.GetResourceAccess() {
			present[resourceAccessKey(key, access)] = true
		}
	}

	for _, resource := range app.GetRequiredResourceAccess() {
		resourceAppID := strings.ToLower(derefString(resource.GetResourceAppId()))

		for _, access := range resource.GetResourceAccess() {
			if present[resourceAccessKey(resourceAppID, access)] {
				continue
			}
			present[resourceAccessKey(resourceAppID, access)] = true

			group, ok := groups[resourceAppID]
			if !ok {
				group = models.NewRequiredResourceAccess()
				preserved := derefString(resource.GetResourceAppId())
				group.SetResourceAppId(&preserved)
				groups[resourceAppID] = group
				merged = append(merged, group)
			}

			group.SetResourceAccess(append(group.GetResourceAccess(), access))
		}
	}

	return merged
}

// resourceAccessKey identifies one permission on one resource. Every component
// is folded: Graph preserves whatever casing another tool or a hand-edited
// manifest wrote, so a "scope" that does not match our "Scope" would look like
// a different permission, get appended a second time by
// mergeExistingResourceAccess, and have the PATCH rejected as a duplicate.
func resourceAccessKey(resourceAppID string, access models.ResourceAccessable) string {
	return strings.ToLower(resourceAppID) + "|" +
		strings.ToLower(uuidString(access.GetId())) + "|" +
		strings.ToLower(derefString(access.GetTypeEscaped()))
}

// ensureServicePrincipalExists creates a service principal for the application if it doesn't exist
// The service principal is required for admin consent to be granted
// Note: This does NOT automatically grant admin consent - that must be done manually in the Azure Portal
func ensureServicePrincipalExists(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig, app models.Applicationable) error {
	if config.Verbose {
		progressln("🔐 Ensuring service principal exists for admin consent...")
	}

	appID := *app.GetAppId()

	// Validate UUID before using in filter
	if _, err := uuid.Parse(appID); err != nil {
		return errors.Wrap(err, "invalid application client ID format")
	}

	filter := fmt.Sprintf("appId eq '%s'", appID)

	servicePrincipals, err := client.ServicePrincipals().Get(ctx, &serviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: &serviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{
			Filter: &filter,
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to check for existing service principal")
	}

	var spObjectID string
	if servicePrincipals == nil || servicePrincipals.GetValue() == nil || len(servicePrincipals.GetValue()) == 0 {
		// Create service principal
		if config.Verbose {
			progressln("   Creating service principal...")
		}

		newSP := models.NewServicePrincipal()
		newSP.SetAppId(&appID)

		createdSP, err := client.ServicePrincipals().Post(ctx, newSP, nil)
		if err != nil {
			return errors.Wrap(err, "failed to create service principal")
		}

		spObjectID = *createdSP.GetId()

		// Add service principal cleanup to rollback
		config.rollback = append(config.rollback, func() error {
			return deleteServicePrincipal(ctx, client, spObjectID, config.Verbose)
		})

		if config.Verbose {
			progressln("   ✅ Service principal created")
		}
	} else if config.Verbose {
		progressln("   ✅ Service principal already exists")
	}

	if config.Verbose {
		// Validate UUID before constructing URL
		if _, err := uuid.Parse(appID); err == nil {
			progressf("✅ Admin consent must be granted manually\n")
			progressf("   Visit: %s\n", adminConsentURL(config.cloudEnvironment().PortalHost, appID))
		}
	}

	return nil
}

// deleteServicePrincipal deletes a service principal (used for rollback)
func deleteServicePrincipal(ctx context.Context, client *msgraphsdk.GraphServiceClient, objectID string, verbose bool) error {
	if verbose {
		progressf("🗑️  Rolling back: Deleting service principal %s\n", objectID)
	}

	err := client.ServicePrincipals().ByServicePrincipalId(objectID).Delete(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to delete service principal during rollback")
	}

	return nil
}
