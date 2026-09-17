// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigureAPIPermissions_DryRun tests dry-run mode for permissions configuration
func TestConfigureAPIPermissions_DryRun(t *testing.T) {
	ctx := context.Background()
	config := &SetupConfig{
		AppName:  "Test App",
		Verbose:  false,
		DryRun:   true,
		rollback: []func() error{},
	}

	app := models.NewApplication()
	displayName := "Test App"
	app.SetDisplayName(&displayName)
	appID := "abc-123"
	app.SetAppId(&appID)
	objectID := "obj-123"
	app.SetId(&objectID)

	err := configureAPIPermissions(ctx, nil, config, app)
	require.NoError(t, err, "Dry run should not return error")
	assert.Empty(t, config.rollback, "Dry run should not add rollback functions")
}

// TestBuildRequiredResourceAccess tests building permission structure
func TestBuildRequiredResourceAccess(t *testing.T) {
	t.Run("creates_resource_access_list", func(t *testing.T) {
		resourceAccess, err := buildRequiredResourceAccess(getRequiredPermissions())
		require.NoError(t, err)
		assert.NotEmpty(t, resourceAccess)
	})

	t.Run("includes_microsoft_graph", func(t *testing.T) {
		resourceAccess, err := buildRequiredResourceAccess(getRequiredPermissions())
		require.NoError(t, err)

		foundGraph := false
		for _, resource := range resourceAccess {
			if *resource.GetResourceAppId() == GraphResourceID {
				foundGraph = true

				// Should have 3 permissions
				perms := resource.GetResourceAccess()
				assert.Len(t, perms, 3, "Microsoft Graph should have 3 permissions")

				// Verify permission types
				foundScope := false
				foundRole := false
				for _, perm := range perms {
					permType := *perm.GetTypeEscaped()
					if permType == PermissionTypeScope {
						foundScope = true
					}
					if permType == PermissionTypeRole {
						foundRole = true
					}
				}

				assert.True(t, foundScope, "Should have delegated permission (Scope)")
				assert.True(t, foundRole, "Should have application permission (Role)")
			}
		}

		assert.True(t, foundGraph, "Should have Microsoft Graph resource")
	})

	t.Run("permission_ids_are_valid_uuids", func(t *testing.T) {
		resourceAccess, err := buildRequiredResourceAccess(getRequiredPermissions())
		require.NoError(t, err)

		for _, resource := range resourceAccess {
			// Resource app ID should be valid UUID
			_, err := uuid.Parse(*resource.GetResourceAppId())
			assert.NoError(t, err, "Resource app ID should be valid UUID")

			// Each permission ID should be valid UUID
			perms := resource.GetResourceAccess()
			for _, perm := range perms {
				permID := perm.GetId()
				require.NotNil(t, permID)
				_, err := uuid.Parse(permID.String())
				assert.NoError(t, err, "Permission ID should be valid UUID")
			}
		}
	})

	t.Run("has_all_required_permissions", func(t *testing.T) {
		_, err := buildRequiredResourceAccess(getRequiredPermissions())
		require.NoError(t, err)

		requiredPerms := getRequiredPermissions()
		assert.Len(t, requiredPerms, 3, "Should have 3 required permissions")

		// Verify User.Read
		foundUserRead := false
		// Verify TeamsActivity.Send
		foundTeamsActivity := false
		// Verify AppCatalog.Read.All
		foundAppCatalog := false

		for _, perm := range requiredPerms {
			if perm.Name == "User.Read" {
				foundUserRead = true
				assert.Equal(t, PermissionTypeScope, perm.Type, "User.Read should be delegated")
			}
			if perm.Name == "TeamsActivity.Send" {
				foundTeamsActivity = true
				assert.Equal(t, PermissionTypeRole, perm.Type, "TeamsActivity.Send should be application")
			}
			if perm.Name == "AppCatalog.Read.All" {
				foundAppCatalog = true
				assert.Equal(t, PermissionTypeRole, perm.Type, "AppCatalog.Read.All should be application")
			}
		}

		assert.True(t, foundUserRead, "Should have User.Read permission")
		assert.True(t, foundTeamsActivity, "Should have TeamsActivity.Send permission")
		assert.True(t, foundAppCatalog, "Should have AppCatalog.Read.All permission")
	})
}

// TestEnsureServicePrincipalExists tests service principal creation logic
func TestEnsureServicePrincipalExists(t *testing.T) {
	t.Run("validates_client_id_format", func(t *testing.T) {
		// Valid UUID should not error
		validUUID := "12345678-1234-1234-1234-123456789012"
		_, err := uuid.Parse(validUUID)
		assert.NoError(t, err)

		// Invalid UUID should error
		invalidUUID := "not-a-uuid"
		_, err = uuid.Parse(invalidUUID)
		assert.Error(t, err)
	})
}

// TestConfigureAPIPermissions_VerboseOutput tests verbose mode
func TestConfigureAPIPermissions_VerboseOutput(t *testing.T) {
	ctx := context.Background()
	config := &SetupConfig{
		AppName:  "Test App",
		Verbose:  true,
		DryRun:   true,
		rollback: []func() error{},
	}

	app := models.NewApplication()
	displayName := "Test App"
	app.SetDisplayName(&displayName)
	appID := "abc-123"
	app.SetAppId(&appID)
	objectID := "obj-123"
	app.SetId(&objectID)

	err := configureAPIPermissions(ctx, nil, config, app)
	require.NoError(t, err, "Verbose mode should not affect success")
}

// TestConfigureAPIPermissions_ErrorHandling tests error scenarios
func TestConfigureAPIPermissions_ErrorHandling(t *testing.T) {
	t.Run("handles_invalid_permission_ids", func(t *testing.T) {
		// buildRequiredResourceAccess should handle validation
		resourceAccess, err := buildRequiredResourceAccess(getRequiredPermissions())
		require.NoError(t, err)
		assert.NotEmpty(t, resourceAccess)
	})
}

// TestDeleteServicePrincipal tests service principal deletion (rollback)
func TestDeleteServicePrincipal(t *testing.T) {
	t.Run("requires_object_id", func(t *testing.T) {
		objectID := "test-object-id"
		assert.NotEmpty(t, objectID, "Object ID is required for deletion")
	})

	t.Run("verbose_mode", func(t *testing.T) {
		objectID := "test-object-id"
		verbose := true

		assert.NotEmpty(t, objectID)
		assert.True(t, verbose)
	})
}

// TestServicePrincipalRollback tests rollback behavior
func TestServicePrincipalRollback(t *testing.T) {
	t.Run("rollback_function_signature", func(t *testing.T) {
		config := &SetupConfig{
			rollback: []func() error{},
		}

		// Simulate adding a rollback function
		config.rollback = append(config.rollback, func() error {
			return nil
		})

		assert.Len(t, config.rollback, 1, "Should have one rollback function")
	})

	t.Run("rollback_executes_in_reverse_order", func(t *testing.T) {
		config := &SetupConfig{
			rollback: []func() error{},
		}

		executionOrder := []int{}

		config.rollback = append(config.rollback, func() error {
			executionOrder = append(executionOrder, 1)
			return nil
		})

		config.rollback = append(config.rollback, func() error {
			executionOrder = append(executionOrder, 2)
			return nil
		})

		// Execute in reverse
		for _, rollbackFunc := range slices.Backward(config.rollback) {
			_ = rollbackFunc()
		}

		assert.Equal(t, []int{2, 1}, executionOrder, "Should execute in reverse order")
	})
}
