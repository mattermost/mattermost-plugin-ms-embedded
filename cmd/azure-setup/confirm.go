// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bufio"
	"os"
	"slices"
	"strings"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/pkg/errors"
)

// preflightPermissionNote explains a permission the plugin does not itself
// need, so the operator approving it knows what it is for and what it costs.
func preflightPermissionNote(perm requiredPermission) string {
	if !slices.Contains(doctorRequirementNames, perm.Name) {
		return ""
	}

	return " - for azure-setup doctor; grants this application's credentials tenant-wide directory read"
}

// showPreflightConfirmation displays a summary of planned changes and prompts for confirmation
func showPreflightConfirmation(config *SetupConfig, existingApp models.Applicationable, permissions []requiredPermission) error {
	progressln("\n" + strings.Repeat("=", 70))
	progressln("🔍 PRE-FLIGHT CHECK")
	progressln(strings.Repeat("=", 70))

	if existingApp != nil {
		progressln("\n📝 Action: Update existing Azure application")
		progressf("   Application Name: %s\n", *existingApp.GetDisplayName())
		progressf("   Application ID:   %s\n", *existingApp.GetAppId())
	} else {
		progressln("\n🆕 Action: Create new Azure application")
		progressf("   Application Name: %s\n", config.AppName)
	}

	progressln("\n📋 Configuration Summary:")
	progressf("   Mattermost Site URL:    %s\n", config.MattermostSiteURL)
	progressf("   Secret Expiration:      %d months\n", config.SecretExpiration)

	progressln("\n🔐 API Permissions to configure:")
	for _, perm := range permissions {
		progressf("   • %s (%s)%s\n", perm.Name, permissionKindLabel(perm.Type), preflightPermissionNote(perm))
	}

	progressln("\n🌐 API Exposure:")
	appIDURI, _ := buildApplicationIDURI(config.MattermostSiteURL, "CLIENT_ID")
	appIDURI = strings.Replace(appIDURI, "CLIENT_ID", "{client-id}", 1)
	progressf("   • Application ID URI: %s\n", appIDURI)
	progressf("   • Scope: %s\n", ScopeName)
	progressf("   • Pre-authorized clients: %d Microsoft apps (Teams, Outlook, Office, Copilot)\n", len(getPreAuthorizedClients()))

	progressln("\n🔑 Operations to perform:")
	if existingApp == nil {
		progressln("   1. Create new Azure AD application")
	} else {
		progressln("   1. Update existing Azure AD application")
	}
	progressln("   2. Configure API permissions")
	progressln("   3. Set up API exposure and scopes")
	progressln("   4. Add pre-authorized Microsoft clients")
	progressln("   5. Generate new client secret")
	progressln("   6. Create service principal (if needed)")

	progressln("\n" + strings.Repeat("=", 70))
	progress("\nProceed with these changes? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return errors.Wrap(err, "failed to read user input")
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response != "y" && response != "yes" {
		progressln("\n❌ Operation cancelled by user")
		return errors.New("operation cancelled by user")
	}

	progressln()
	return nil
}
