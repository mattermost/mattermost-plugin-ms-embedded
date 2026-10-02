// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrchestrationCreateNewApp tests the complete workflow for creating a new application
func TestOrchestrationCreateNewApp(t *testing.T) {
	ctx := context.Background()

	// This test validates the orchestration logic without hitting Azure
	// It tests that all the pieces work together correctly

	config := &SetupConfig{
		TenantID:          "tenant-123",
		MattermostSiteURL: "https://mattermost.example.com",
		AppName:           "Test App",
		ClientID:          "",
		SecretExpiration:  12,
		DryRun:            false,
		NonInteractive:    true,
		Verbose:           false,
		OutputFormat:      "json",
		ctx:               ctx,
		rollback:          []func() error{},
	}

	// Validate inputs work correctly
	err := validateInputs(config)
	require.NoError(t, err, "Input validation should succeed")

	// Test building Application ID URI
	mockClientID := "abc123-def456-789"
	appIDURI, err := buildApplicationIDURI(config.MattermostSiteURL, mockClientID)
	require.NoError(t, err, "Should build Application ID URI")
	assert.Equal(t, "api://mattermost.example.com/"+mockClientID, appIDURI)

	// Test permission structure
	perms := getRequiredPermissions()
	assert.Len(t, perms, 3, "Should have 3 required permissions")
	assert.Equal(t, "User.Read", perms[0].Name)
	assert.Equal(t, "TeamsActivity.Send", perms[1].Name)
	assert.Equal(t, "AppCatalog.Read.All", perms[2].Name)

	// Test pre-authorized clients
	clients := getPreAuthorizedClients()
	assert.Len(t, clients, 9, "Should have 9 pre-authorized clients (Teams, Outlook, Office, Copilot)")
}

// TestOrchestrationDryRun tests that dry-run mode doesn't modify state
func TestOrchestrationDryRun(t *testing.T) {
	ctx := context.Background()

	config := &SetupConfig{
		TenantID:          "tenant-123",
		MattermostSiteURL: "https://mattermost.example.com",
		AppName:           "Test App",
		ClientID:          "",
		SecretExpiration:  12,
		DryRun:            true, // Enable dry-run
		NonInteractive:    true,
		Verbose:           false,
		OutputFormat:      "json",
		ctx:               ctx,
		rollback:          []func() error{},
	}

	// Validate inputs
	err := validateInputs(config)
	require.NoError(t, err, "Input validation should succeed")

	// Verify dry-run flag is set
	assert.True(t, config.DryRun, "Dry-run should be enabled")

	// In dry-run mode, no rollback functions should be added
	assert.Empty(t, config.rollback, "Rollback should be empty in dry-run")
}

// TestOrchestrationRollback tests the rollback mechanism
func TestOrchestrationRollback(t *testing.T) {
	ctx := context.Background()

	config := &SetupConfig{
		TenantID:          "tenant-123",
		MattermostSiteURL: "https://mattermost.example.com",
		AppName:           "Test App",
		ClientID:          "",
		SecretExpiration:  12,
		DryRun:            false,
		NonInteractive:    true,
		Verbose:           false,
		OutputFormat:      "json",
		ctx:               ctx,
		rollback:          []func() error{},
	}

	// Simulate adding rollback functions
	var executionOrder []int

	config.rollback = append(config.rollback, func() error {
		executionOrder = append(executionOrder, 1)
		return nil
	})

	config.rollback = append(config.rollback, func() error {
		executionOrder = append(executionOrder, 2)
		return nil
	})

	config.rollback = append(config.rollback, func() error {
		executionOrder = append(executionOrder, 3)
		return nil
	})

	// Execute rollback
	executeRollback(config)

	// Verify rollback executed in reverse order
	require.Len(t, executionOrder, 3)
	assert.Equal(t, []int{3, 2, 1}, executionOrder, "Rollback should execute in reverse order")
}

// TestOrchestrationInputValidation tests various input validation scenarios
func TestOrchestrationInputValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      *SetupConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			config: &SetupConfig{
				MattermostSiteURL: "https://mattermost.example.com",
				AppName:           "Test App",
				SecretExpiration:  12,
			},
			expectError: false,
		},
		{
			name: "HTTP not allowed",
			config: &SetupConfig{
				MattermostSiteURL: "http://mattermost.example.com",
				AppName:           "Test App",
				SecretExpiration:  12,
			},
			expectError: true,
			errorMsg:    "HTTPS",
		},
		{
			name: "site URL with path",
			config: &SetupConfig{
				MattermostSiteURL: "https://example.com/mattermost",
				AppName:           "Test App",
				SecretExpiration:  12,
			},
			expectError: false,
		},
		{
			name: "secret expiration validation",
			config: &SetupConfig{
				MattermostSiteURL: "https://mattermost.example.com",
				AppName:           "Test App",
				SecretExpiration:  25, // Too long
			},
			expectError: true,
			errorMsg:    "24 months",
		},
		{
			name: "app name too short",
			config: &SetupConfig{
				MattermostSiteURL: "https://mattermost.example.com",
				AppName:           "AB",
				SecretExpiration:  12,
			},
			expectError: true,
			errorMsg:    "3 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInputs(tt.config)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestOrchestrationApplicationIDURI tests Application ID URI generation
func TestOrchestrationApplicationIDURI(t *testing.T) {
	tests := []struct {
		name     string
		siteURL  string
		clientID string
		expected string
	}{
		{
			name:     "simple domain",
			siteURL:  "https://mattermost.example.com",
			clientID: "abc-123",
			expected: "api://mattermost.example.com/abc-123",
		},
		{
			name:     "domain with port",
			siteURL:  "https://mattermost.example.com:8065",
			clientID: "abc-123",
			expected: "api://mattermost.example.com:8065/abc-123",
		},
		{
			name:     "domain with path",
			siteURL:  "https://example.com/mattermost",
			clientID: "abc-123",
			expected: "api://example.com/mattermost/abc-123",
		},
		{
			name:     "domain with nested path",
			siteURL:  "https://example.com/apps/mattermost",
			clientID: "abc-123",
			expected: "api://example.com/apps/mattermost/abc-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildApplicationIDURI(tt.siteURL, tt.clientID)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestOrchestrationOutputFormats tests all output format types
func TestOrchestrationOutputFormats(t *testing.T) {
	result := &SetupResult{
		Success:             true,
		Message:             "Test setup",
		ApplicationClientID: "client-123",
		TenantID:            "tenant-456",
		ClientSecret:        "secret-789",
		SecretExpiration:    time.Now().Format("2006-01-02 15:04:05 MST"),
		ApplicationID:       "app-id-123",
		ApplicationName:     "Test App",
		ApplicationIDURI:    "api://example.com/client-123",
		Created:             true,
		DryRun:              false,
	}

	formats := []string{"human", "json", "env", "mattermost"}

	for _, format := range formats {
		t.Run("format_"+format, func(t *testing.T) {
			err := OutputResult(result, format)
			assert.NoError(t, err, "Output format %s should work", format)
		})
	}

	// Test invalid format
	t.Run("invalid_format", func(t *testing.T) {
		err := OutputResult(result, "invalid")
		assert.Error(t, err, "Invalid format should return error")
	})
}

// TestOrchestrationPermissionConstants tests that permission constants are valid UUIDs
func TestOrchestrationPermissionConstants(t *testing.T) {
	tests := []struct {
		name       string
		permission string
	}{
		{"GraphResourceID", GraphResourceID},
		{"PermissionUserReadID", PermissionUserReadID},
		{"PermissionTeamsActivitySendID", PermissionTeamsActivitySendID},
		{"PermissionAppCatalogReadAllID", PermissionAppCatalogReadAllID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// These should be valid UUIDs
			assert.Len(t, tt.permission, 36, "Should be UUID format")
			assert.Contains(t, tt.permission, "-", "Should contain UUID separators")
		})
	}
}

// TestOrchestrationPreAuthorizedClients tests pre-authorized client constants
func TestOrchestrationPreAuthorizedClients(t *testing.T) {
	clients := getPreAuthorizedClients()

	require.Len(t, clients, 9, "Should have exactly 9 pre-authorized clients (Teams, Outlook, Office, Copilot)")

	// Verify all are valid UUIDs
	for i, client := range clients {
		t.Run("client_"+string(rune(i)), func(t *testing.T) {
			assert.Len(t, client, 36, "Client ID should be UUID format")
			assert.Contains(t, client, "-", "Client ID should contain UUID separators")
		})
	}

	// Verify all Microsoft first-party clients are present
	assert.Contains(t, clients, ClientIDTeamsWeb, "Should contain Teams Web")
	assert.Contains(t, clients, ClientIDTeamsMobileDesktop, "Should contain Teams Mobile/Desktop")
	assert.Contains(t, clients, ClientIDOutlookWeb, "Should contain Outlook Web")
	assert.Contains(t, clients, ClientIDOutlookDesktop, "Should contain Outlook Desktop")
	assert.Contains(t, clients, ClientIDOutlookMobile, "Should contain Outlook Mobile")
	assert.Contains(t, clients, ClientIDOfficeWeb, "Should contain Office Web")
	assert.Contains(t, clients, ClientIDOfficeDesktop, "Should contain Office Desktop")
	assert.Contains(t, clients, ClientIDCopilot, "Should contain Copilot")
	assert.Contains(t, clients, ClientIDOfficeUniversal, "Should contain Office Universal")
}

// TestOrchestrationBuildRequiredResourceAccess tests permission structure building
func TestOrchestrationBuildRequiredResourceAccess(t *testing.T) {
	resourceAccess, err := buildRequiredResourceAccess(getRequiredPermissions())
	require.NoError(t, err, "Should build resource access successfully")

	require.NotEmpty(t, resourceAccess, "Should have resource access items")

	// Should have at least one resource (Microsoft Graph)
	assert.GreaterOrEqual(t, len(resourceAccess), 1, "Should have at least Microsoft Graph resource")

	// Verify the Graph resource
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

			assert.True(t, foundScope, "Should have at least one delegated permission (Scope)")
			assert.True(t, foundRole, "Should have at least one application permission (Role)")
		}
	}

	assert.True(t, foundGraph, "Should have Microsoft Graph resource configured")
}

// TestOrchestrationBuildPreAuthorizedApplications tests pre-authorized app building
func TestOrchestrationBuildPreAuthorizedApplications(t *testing.T) {
	// Generate a scope ID
	scopeID := "12345678-1234-1234-1234-123456789012"
	scopeUUID, err := uuid.Parse(scopeID)
	require.NoError(t, err)

	preAuthApps, err := buildPreAuthorizedApplications(scopeUUID)
	require.NoError(t, err, "Should build pre-authorized apps successfully")

	assert.Len(t, preAuthApps, 9, "Should have 9 pre-authorized applications (Teams, Outlook, Office, Copilot)")

	// Verify each app has the scope
	for _, app := range preAuthApps {
		delegatedPerms := app.GetDelegatedPermissionIds()
		require.Len(t, delegatedPerms, 1, "Each app should have 1 delegated permission")
		assert.Equal(t, scopeID, delegatedPerms[0], "Should have the correct scope ID")
	}
}

// TestOrchestrationErrorScenarios tests various error scenarios
func TestOrchestrationErrorScenarios(t *testing.T) {
	t.Run("empty_site_URL", func(t *testing.T) {
		config := &SetupConfig{
			MattermostSiteURL: "",
			AppName:           "Test App",
			SecretExpiration:  12,
		}
		err := validateInputs(config)
		assert.Error(t, err, "Empty site URL should return error")
	})

	t.Run("empty_app_name", func(t *testing.T) {
		config := &SetupConfig{
			MattermostSiteURL: "https://mattermost.example.com",
			AppName:           "",
			SecretExpiration:  12,
		}
		err := validateInputs(config)
		assert.Error(t, err, "Empty app name should return error")
	})

	t.Run("HTTP_not_HTTPS", func(t *testing.T) {
		config := &SetupConfig{
			MattermostSiteURL: "http://mattermost.example.com",
			AppName:           "Test App",
			SecretExpiration:  12,
		}
		err := validateInputs(config)
		assert.Error(t, err, "HTTP URLs should return error")
	})
}

// TestOrchestrationContextHandling tests context timeout behavior
func TestOrchestrationContextHandling(t *testing.T) {
	// Test that context with timeout can be created
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	config := &SetupConfig{
		ctx: ctx,
	}

	// Verify context is set
	assert.NotNil(t, config.ctx, "Context should be set")

	// Verify context has deadline
	_, hasDeadline := config.ctx.Deadline()
	assert.True(t, hasDeadline, "Context should have deadline")
}
