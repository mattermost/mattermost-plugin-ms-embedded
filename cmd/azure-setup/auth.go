// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/cloudenv"
)

// authenticateToAzure establishes authentication to Azure using available methods
// Priority order: Environment variables -> Azure CLI -> Interactive browser
func authenticateToAzure(ctx context.Context, env cloudenv.Environment, tenantID string, verbose bool) (azcore.TokenCredential, error) {
	if verbose {
		progressln("🔐 Authenticating to Azure...")
	}

	// Try multiple authentication methods in order of preference
	credential, method, err := tryAuthenticationMethods(ctx, env, tenantID, verbose)
	if err != nil {
		return nil, errors.Wrap(err, "failed to authenticate to Azure")
	}

	if verbose {
		progressf("✅ Successfully authenticated using: %s\n", method)
	}

	return credential, nil
}

// tryAuthenticationMethods attempts different authentication methods
func tryAuthenticationMethods(ctx context.Context, env cloudenv.Environment, tenantID string, verbose bool) (azcore.TokenCredential, string, error) {
	// Method 1: Try environment variables (service principal)
	if verbose {
		progressln("   Trying: Environment variables (Service Principal)...")
	}
	if cred, err := tryEnvironmentCredential(env); err == nil {
		if err := testCredential(ctx, env, cred); err == nil {
			return cred, "Environment Variables (Service Principal)", nil
		}
	}

	// Method 2: Try Azure CLI
	if verbose {
		progressln("   Trying: Azure CLI...")
	}
	if cred, err := tryAzureCLICredential(tenantID); err == nil {
		if err := testCredential(ctx, env, cred); err == nil {
			return cred, "Azure CLI", nil
		}
	}

	// Method 3: Try interactive browser
	if verbose {
		progressln("   Trying: Interactive browser...")
	}
	if cred, err := tryInteractiveBrowserCredential(env, tenantID); err == nil {
		if err := testCredential(ctx, env, cred); err == nil {
			return cred, "Interactive Browser", nil
		}
	}

	// Method 4: Device code flow (last resort, works on headless systems)
	if verbose {
		progressln("   Trying: Device code flow...")
	}
	if cred, err := tryDeviceCodeCredential(env, tenantID); err == nil {
		if err := testCredential(ctx, env, cred); err == nil {
			return cred, "Device Code Flow", nil
		}
	}

	return nil, "", errors.New("all authentication methods failed")
}

// tryEnvironmentCredential attempts to authenticate using environment variables.
// EnvironmentCredential reads tenant configuration from AZURE_TENANT_ID and does
// not expose AdditionallyAllowedTenants via options, so no tenantID parameter is
// accepted here. The national cloud is applied via ClientOptions.Cloud.
func tryEnvironmentCredential(env cloudenv.Environment) (azcore.TokenCredential, error) {
	opts := &azidentity.EnvironmentCredentialOptions{}
	opts.Cloud = env.AzureCloud

	cred, err := azidentity.NewEnvironmentCredential(opts)
	if err != nil {
		return nil, err
	}

	return cred, nil
}

// tryAzureCLICredential attempts to authenticate using Azure CLI.
// AzureCLICredentialOptions does not expose a cloud setting: the Azure CLI
// credential inherits whatever cloud the operator has selected via
// `az cloud set` (e.g. `az cloud set --name AzureUSGovernment` for GCC High/DoD).
func tryAzureCLICredential(tenantID string) (azcore.TokenCredential, error) {
	opts := &azidentity.AzureCLICredentialOptions{}
	if tenantID != "" {
		opts.TenantID = tenantID
	}

	cred, err := azidentity.NewAzureCLICredential(opts)
	if err != nil {
		return nil, err
	}

	return cred, nil
}

// tryInteractiveBrowserCredential attempts to authenticate using interactive browser
func tryInteractiveBrowserCredential(env cloudenv.Environment, tenantID string) (azcore.TokenCredential, error) {
	opts := &azidentity.InteractiveBrowserCredentialOptions{}
	opts.Cloud = env.AzureCloud
	if tenantID != "" {
		opts.TenantID = tenantID
	}

	cred, err := azidentity.NewInteractiveBrowserCredential(opts)
	if err != nil {
		return nil, err
	}

	return cred, nil
}

// tryDeviceCodeCredential attempts to authenticate using device code flow
func tryDeviceCodeCredential(env cloudenv.Environment, tenantID string) (azcore.TokenCredential, error) {
	opts := &azidentity.DeviceCodeCredentialOptions{
		UserPrompt: func(ctx context.Context, message azidentity.DeviceCodeMessage) error {
			// stderr, so the prompt reaches the operator without corrupting a
			// report being piped from stdout (doctor -o json).
			progressln("\n" + message.Message)
			return nil
		},
	}
	opts.Cloud = env.AzureCloud
	if tenantID != "" {
		opts.TenantID = tenantID
	}

	cred, err := azidentity.NewDeviceCodeCredential(opts)
	if err != nil {
		return nil, err
	}

	return cred, nil
}

// testCredential verifies that a credential can obtain a token
func testCredential(ctx context.Context, env cloudenv.Environment, cred azcore.TokenCredential) error {
	// Try to get a token for Microsoft Graph
	_, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{env.GraphScope},
	})
	return err
}

// validateAzureConnection ensures we can connect to Azure and get basic info
func validateAzureConnection(ctx context.Context, env cloudenv.Environment, cred azcore.TokenCredential, verbose bool) error {
	if verbose {
		progressln("🔍 Validating Azure connection...")
	}

	// Try to get a token to verify the connection works
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{env.GraphScope},
	})
	if err != nil {
		return errors.Wrap(err, "failed to obtain access token from Azure")
	}

	if token.Token == "" {
		return errors.New("received empty access token from Azure")
	}

	if verbose {
		progressln("✅ Azure connection validated")
	}

	return nil
}

// newGraphClient builds a Microsoft Graph client bound to the given national
// cloud. The cloud's Graph scope is requested so the token audience matches the
// cloud's Graph endpoint (for example graph.microsoft.us), and the adapter base
// URL is set explicitly because DoD uses a different Graph host than GCC High.
func newGraphClient(env cloudenv.Environment, cred azcore.TokenCredential) (*msgraphsdk.GraphServiceClient, error) {
	authProvider, err := authentication.NewAzureIdentityAuthenticationProviderWithScopes(cred, []string{env.GraphScope})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create auth provider")
	}

	adapter, err := msgraphsdk.NewGraphRequestAdapter(authProvider)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Graph adapter")
	}
	adapter.SetBaseUrl(env.GraphBaseURL)

	return msgraphsdk.NewGraphServiceClient(adapter), nil
}
