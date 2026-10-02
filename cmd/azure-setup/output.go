// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

// OutputResult displays the setup result in the requested format
func OutputResult(result *SetupResult, format string) error {
	switch strings.ToLower(format) {
	case "json":
		return outputJSON(result)
	case "env":
		return outputEnv(result)
	case "mattermost":
		return outputMattermostConfig(result)
	case "human", "":
		return outputHuman(result)
	default:
		return errors.Errorf("unknown output format: %s", format)
	}
}

// outputHuman outputs results in human-readable format
func outputHuman(result *SetupResult) error {
	fmt.Println("\n" + strings.Repeat("=", 70))
	switch {
	case result.DryRun:
		fmt.Println("🔍 DRY RUN COMPLETE - No changes were made")
	case result.Success:
		fmt.Println("✅ AZURE SETUP COMPLETE")
	default:
		fmt.Println("❌ AZURE SETUP FAILED")
	}
	fmt.Println(strings.Repeat("=", 70))

	if !result.Success {
		fmt.Printf("\nError: %s\n", result.Message)
		return nil
	}

	if result.Created {
		fmt.Println("\n📝 A new Azure application has been created")
	} else {
		fmt.Println("\n📝 An existing Azure application has been updated")
	}

	fmt.Println("\n🔐 CREDENTIALS FOR MATTERMOST CONFIGURATION")
	fmt.Println(strings.Repeat("-", 70))
	fmt.Printf("Tenant ID:             %s\n", result.TenantID)
	fmt.Printf("Application Client ID: %s\n", result.ApplicationClientID)
	fmt.Printf("Client Secret:         %s\n", result.ClientSecret)
	fmt.Printf("Secret Expires:        %s\n", result.SecretExpiration)
	fmt.Println(strings.Repeat("-", 70))

	fmt.Println("\n📋 APPLICATION DETAILS")
	fmt.Println(strings.Repeat("-", 70))
	fmt.Printf("Application Name:      %s\n", result.ApplicationName)
	fmt.Printf("Application ID (UUID): %s\n", result.ApplicationID)
	fmt.Printf("Application ID URI:    %s\n", result.ApplicationIDURI)
	fmt.Println(strings.Repeat("-", 70))

	fmt.Println("\n⚠️  IMPORTANT SECURITY NOTICE")
	fmt.Println(strings.Repeat("-", 70))
	fmt.Println("• Store the client secret securely - it will not be shown again")
	fmt.Println("• The client secret is sensitive and should be treated like a password")
	fmt.Println("• Rotate secrets before they expire to prevent service disruption")
	fmt.Println(strings.Repeat("-", 70))

	fmt.Println("\n📝 NEXT STEPS")
	fmt.Println(strings.Repeat("-", 70))
	fmt.Println("1. Grant admin consent for API permissions:")
	if result.DoctorRequirements {
		fmt.Println("   (this also consents to the read-only permissions the doctor needs)")
	}
	fmt.Printf("   %s\n", adminConsentURL(result.PortalHost, result.ApplicationClientID))
	fmt.Println("\n2. Configure the Mattermost plugin with these values:")
	fmt.Println("   - System Console > Plugins > Mattermost Embedded")
	fmt.Println("   - Enter the Tenant ID, Client ID, and Client Secret above")
	fmt.Println("\n3. Download and install the Teams app manifest:")
	fmt.Println("   - In Mattermost, go to the plugin settings")
	fmt.Println("   - Download the app manifest")
	fmt.Println("   - Upload it to Microsoft Teams Admin Center")
	fmt.Println(strings.Repeat("-", 70))

	return nil
}

// outputJSON outputs results in JSON format
func outputJSON(result *SetupResult) error {
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(jsonBytes))
	return nil
}

// outputEnv outputs results as environment variables
func outputEnv(result *SetupResult) error {
	if !result.Success {
		return errors.Errorf("setup failed: %s", result.Message)
	}

	fmt.Println("# Azure AD / Microsoft 365 Configuration")
	fmt.Println("# Add these to your environment or .env file")
	fmt.Println()
	fmt.Printf("export M365_TENANT_ID=\"%s\"\n", result.TenantID)
	fmt.Printf("export M365_CLIENT_ID=\"%s\"\n", result.ApplicationClientID)
	fmt.Printf("export M365_CLIENT_SECRET=\"%s\"\n", result.ClientSecret)
	fmt.Println()
	fmt.Println("# Mattermost Plugin Configuration")
	fmt.Printf("export MM_PLUGIN_MSTEAMS_TENANT_ID=\"%s\"\n", result.TenantID)
	fmt.Printf("export MM_PLUGIN_MSTEAMS_CLIENT_ID=\"%s\"\n", result.ApplicationClientID)
	fmt.Printf("export MM_PLUGIN_MSTEAMS_CLIENT_SECRET=\"%s\"\n", result.ClientSecret)
	fmt.Printf("export MM_PLUGIN_MSTEAMS_APP_NAME=\"%s\"\n", result.ApplicationName)
	fmt.Println()
	fmt.Printf("# Secret expires on: %s\n", result.SecretExpiration)

	return nil
}

// outputMattermostConfig outputs results in Mattermost config.json format
func outputMattermostConfig(result *SetupResult) error {
	if !result.Success {
		return errors.Errorf("setup failed: %s", result.Message)
	}

	config := map[string]any{
		"PluginSettings": map[string]any{
			"Plugins": map[string]any{
				PluginID: map[string]any{
					"m365_tenant_id":     result.TenantID,
					"m365_client_id":     result.ApplicationClientID,
					"m365_client_secret": result.ClientSecret,
					"app_name":           result.ApplicationName,
				},
			},
		},
	}

	jsonBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println("# Add this to your Mattermost config.json")
	fmt.Println("# (merge with existing configuration)")
	fmt.Println()
	fmt.Println(string(jsonBytes))
	fmt.Println()
	fmt.Printf("# Secret expires on: %s\n", result.SecretExpiration)

	return nil
}
