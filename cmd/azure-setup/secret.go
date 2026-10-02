// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"fmt"
	"time"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/pkg/errors"
)

// generateClientSecret generates a new client secret for the application
func generateClientSecret(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig, app models.Applicationable) (string, time.Time, error) {
	if config.Verbose {
		progressln("🔑 Generating client secret...")
	}

	if config.DryRun {
		progressf("   [DRY RUN] Would generate client secret (expires in %d months)\n", config.SecretExpiration)
		// Return mock values for dry run
		expirationDate := time.Now().AddDate(0, config.SecretExpiration, 0)
		return "mock-secret-value-for-dry-run", expirationDate, nil
	}

	// Calculate expiration date
	expirationDate := time.Now().AddDate(0, config.SecretExpiration, 0)

	// Create the password credential (client secret)
	passwordCredential := models.NewPasswordCredential()

	displayName := fmt.Sprintf("Mattermost Plugin Secret (expires %s)", expirationDate.Format("2006-01-02"))
	passwordCredential.SetDisplayName(&displayName)

	passwordCredential.SetEndDateTime(&expirationDate)

	// Add the password credential to the application
	requestBody := applications.NewItemAddPasswordPostRequestBody()
	requestBody.SetPasswordCredential(passwordCredential)

	result, err := client.Applications().ByApplicationId(*app.GetId()).AddPassword().Post(ctx, requestBody, nil)
	if err != nil {
		return "", time.Time{}, errors.Wrap(err, "failed to generate client secret")
	}

	secretValue := *result.GetSecretText()

	if config.Verbose {
		progressln("✅ Client secret generated successfully")
		progressf("   Expires: %s\n", expirationDate.Format("2006-01-02 15:04:05 MST"))
		progressln("   ⚠️  WARNING: Save this secret securely - it will not be shown again!")
	}

	return secretValue, expirationDate, nil
}
