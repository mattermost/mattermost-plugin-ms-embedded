// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputJSON(t *testing.T) {
	result := &SetupResult{
		Success:             true,
		Message:             "Setup completed successfully",
		ApplicationClientID: "abc123-def456",
		TenantID:            "tenant-123",
		ClientSecret:        "secret-value",
		SecretExpiration:    "2026-01-15 12:00:00 UTC",
		ApplicationID:       "app-id-123",
		ApplicationName:     "Mattermost for Teams",
		ApplicationIDURI:    "api://mattermost.example.com/abc123-def456",
		Created:             true,
		DryRun:              false,
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := outputJSON(result)
	require.NoError(t, err)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	// Verify it's valid JSON
	var parsed SetupResult
	err = json.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err, "output should be valid JSON")

	// Verify all fields are present
	assert.Equal(t, result.Success, parsed.Success)
	assert.Equal(t, result.ApplicationClientID, parsed.ApplicationClientID)
	assert.Equal(t, result.TenantID, parsed.TenantID)
	assert.Equal(t, result.ClientSecret, parsed.ClientSecret)
	assert.Equal(t, result.ApplicationName, parsed.ApplicationName)
	assert.Equal(t, result.Created, parsed.Created)
}

func TestOutputEnv(t *testing.T) {
	result := &SetupResult{
		Success:             true,
		Message:             "Setup completed successfully",
		ApplicationClientID: "abc123-def456",
		TenantID:            "tenant-123",
		ClientSecret:        "secret-value",
		SecretExpiration:    "2026-01-15 12:00:00 UTC",
		ApplicationID:       "app-id-123",
		ApplicationName:     "Mattermost for Teams",
		ApplicationIDURI:    "api://mattermost.example.com/abc123-def456",
		Created:             true,
		DryRun:              false,
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	err = outputEnv(result)
	require.NoError(t, err)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	// Verify it contains export statements
	assert.Contains(t, output, "export M365_TENANT_ID=")
	assert.Contains(t, output, "export M365_CLIENT_ID=")
	assert.Contains(t, output, "export M365_CLIENT_SECRET=")
	assert.Contains(t, output, "export MM_PLUGIN_MSTEAMS_TENANT_ID=")
	assert.Contains(t, output, "export MM_PLUGIN_MSTEAMS_CLIENT_ID=")
	assert.Contains(t, output, "export MM_PLUGIN_MSTEAMS_CLIENT_SECRET=")

	// Verify values are present
	assert.Contains(t, output, result.TenantID)
	assert.Contains(t, output, result.ApplicationClientID)
	assert.Contains(t, output, result.ClientSecret)
	assert.Contains(t, output, result.ApplicationName)
}

func TestOutputEnvFailure(t *testing.T) {
	result := &SetupResult{
		Success: false,
		Message: "Setup failed",
	}

	err := outputEnv(result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "setup failed")
}

func TestOutputMattermostConfig(t *testing.T) {
	result := &SetupResult{
		Success:             true,
		Message:             "Setup completed successfully",
		ApplicationClientID: "abc123-def456",
		TenantID:            "tenant-123",
		ClientSecret:        "secret-value",
		SecretExpiration:    "2026-01-15 12:00:00 UTC",
		ApplicationID:       "app-id-123",
		ApplicationName:     "Mattermost for Teams",
		ApplicationIDURI:    "api://mattermost.example.com/abc123-def456",
		Created:             true,
		DryRun:              false,
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	err = outputMattermostConfig(result)
	require.NoError(t, err)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	// Verify it contains JSON config
	assert.Contains(t, output, "PluginSettings")
	assert.Contains(t, output, PluginID)
	assert.Contains(t, output, "m365_tenant_id")
	assert.Contains(t, output, "m365_client_id")
	assert.Contains(t, output, "m365_client_secret")

	// Extract JSON portion
	jsonStart := strings.Index(output, "{")
	jsonEnd := strings.LastIndex(output, "}")
	if jsonStart != -1 && jsonEnd != -1 {
		jsonStr := output[jsonStart : jsonEnd+1]

		// Verify it's valid JSON
		var parsed map[string]any
		err = json.Unmarshal([]byte(jsonStr), &parsed)
		require.NoError(t, err, "output should contain valid JSON")
	}
}

func TestOutputMattermostConfigFailure(t *testing.T) {
	result := &SetupResult{
		Success: false,
		Message: "Setup failed",
	}

	err := outputMattermostConfig(result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "setup failed")
}

func TestOutputHuman(t *testing.T) {
	tests := []struct {
		name     string
		result   *SetupResult
		contains []string
	}{
		{
			name: "successful creation",
			result: &SetupResult{
				Success:             true,
				Message:             "Setup completed successfully",
				ApplicationClientID: "abc123-def456",
				TenantID:            "tenant-123",
				ClientSecret:        "secret-value",
				SecretExpiration:    "2026-01-15 12:00:00 UTC",
				ApplicationID:       "app-id-123",
				ApplicationName:     "Mattermost for Teams",
				ApplicationIDURI:    "api://mattermost.example.com/abc123-def456",
				Created:             true,
				DryRun:              false,
			},
			contains: []string{
				"AZURE SETUP COMPLETE",
				"new Azure application has been created",
				"Tenant ID:",
				"Application Client ID:",
				"Client Secret:",
				"NEXT STEPS",
				"Grant admin consent",
				"outside the tenant where it is installed",
				"Multitenant",
				"ApplicationMenuBlade/~/Authentication/appId/abc123-def456",
			},
		},
		{
			name: "successful update",
			result: &SetupResult{
				Success:             true,
				Message:             "Setup completed successfully",
				ApplicationClientID: "abc123-def456",
				TenantID:            "tenant-123",
				ClientSecret:        "secret-value",
				SecretExpiration:    "2026-01-15 12:00:00 UTC",
				ApplicationID:       "app-id-123",
				ApplicationName:     "Mattermost for Teams",
				ApplicationIDURI:    "api://mattermost.example.com/abc123-def456",
				Created:             false,
				DryRun:              false,
			},
			contains: []string{
				"AZURE SETUP COMPLETE",
				"existing Azure application has been updated",
				"Tenant ID:",
				"Application Client ID:",
				"outside the tenant where it is installed",
				"Multitenant",
			},
		},
		{
			name: "dry run",
			result: &SetupResult{
				Success:             true,
				Message:             "Setup completed successfully",
				ApplicationClientID: "abc123-def456",
				TenantID:            "tenant-123",
				ClientSecret:        "***DRY-RUN-NO-SECRET-GENERATED***",
				SecretExpiration:    "2026-01-15 12:00:00 UTC",
				ApplicationID:       "app-id-123",
				ApplicationName:     "Mattermost for Teams",
				ApplicationIDURI:    "api://mattermost.example.com/abc123-def456",
				Created:             true,
				DryRun:              true,
			},
			contains: []string{
				"DRY RUN COMPLETE",
				"No changes were made",
			},
		},
		{
			name: "failure",
			result: &SetupResult{
				Success: false,
				Message: "Failed to create application",
			},
			contains: []string{
				"AZURE SETUP FAILED",
				"Failed to create application",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err := outputHuman(tt.result)
			require.NoError(t, err)

			_ = w.Close()
			os.Stdout = oldStdout

			var buf bytes.Buffer
			_, _ = io.Copy(&buf, r)
			output := buf.String()

			// Verify expected strings are present
			for _, expected := range tt.contains {
				assert.Contains(t, output, expected, "output should contain: %s", expected)
			}
		})
	}
}

func TestOutputResult(t *testing.T) {
	result := &SetupResult{
		Success:             true,
		Message:             "Setup completed successfully",
		ApplicationClientID: "abc123-def456",
		TenantID:            "tenant-123",
		ClientSecret:        "secret-value",
		SecretExpiration:    "2026-01-15 12:00:00 UTC",
		ApplicationID:       "app-id-123",
		ApplicationName:     "Mattermost for Teams",
		ApplicationIDURI:    "api://mattermost.example.com/abc123-def456",
		Created:             true,
		DryRun:              false,
	}

	tests := []struct {
		name        string
		format      string
		expectError bool
	}{
		{
			name:        "human format",
			format:      "human",
			expectError: false,
		},
		{
			name:        "json format",
			format:      "json",
			expectError: false,
		},
		{
			name:        "env format",
			format:      "env",
			expectError: false,
		},
		{
			name:        "mattermost format",
			format:      "mattermost",
			expectError: false,
		},
		{
			name:        "empty format defaults to human",
			format:      "",
			expectError: false,
		},
		{
			name:        "invalid format",
			format:      "invalid",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err := OutputResult(result, tt.format)

			_ = w.Close()
			os.Stdout = oldStdout

			// Drain the pipe
			_, _ = io.Copy(io.Discard, r)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
