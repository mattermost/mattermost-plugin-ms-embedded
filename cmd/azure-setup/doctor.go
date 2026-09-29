// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	abstractions "github.com/microsoft/kiota-abstractions-go"
	"github.com/microsoft/kiota-abstractions-go/serialization"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"
	"github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/cloudenv"
)

// doctorApplicationSelect lists every application property the doctor inspects;
// Graph omits most unless selected.
var doctorApplicationSelect = []string{
	"id",
	"appId",
	"displayName",
	"signInAudience",
	"identifierUris",
	"api",
	"requiredResourceAccess",
	"passwordCredentials",
	"keyCredentials",
	"createdDateTime",
}

// runDoctor inspects an existing Azure application registration and prints a
// report describing whether it is configured the way the plugin needs.
func runDoctor(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env, err := resolveCloud(flagCloud)
	if err != nil {
		return errors.Wrap(err, "invalid input")
	}

	// Validate every flag before the (possibly interactive) login.
	if _, err = normalizeReportFormat(flagOutputFormat); err != nil {
		return errors.Wrap(err, "invalid input")
	}

	if flagSiteURL != "" {
		if err = validateSiteURL(flagSiteURL); err != nil {
			return errors.Wrap(err, "invalid --site-url")
		}
	}

	if flagManifest != "" {
		if _, err = os.Stat(flagManifest); err != nil {
			return errors.Wrap(err, "invalid --manifest")
		}
	}

	if flagSecretWarningDays < 0 {
		return errors.Errorf("invalid --secret-warning-days %d: must be zero or greater (zero suppresses expiry warnings)", flagSecretWarningDays)
	}

	report := &DoctorReport{
		GeneratedAt:       time.Now().UTC().Format("2006-01-02 15:04:05 MST"),
		ToolVersion:       version,
		Cloud:             env.Name,
		TenantID:          flagTenantID,
		MattermostSiteURL: flagSiteURL,
		PortalHost:        env.PortalHost,
	}

	progressln("🩺 Running Azure configuration doctor...")

	manifest, manifestErr := loadDoctorManifest(report)

	client, err := connectDoctor(ctx, env, report)
	if err != nil {
		return err
	}

	if err = inspectApplication(ctx, client, env, report, manifest, manifestErr); err != nil {
		return err
	}

	report.Finalize()

	if err = emitDoctorReport(report, flagOutputFormat, flagReportFile); err != nil {
		return err
	}

	if report.Summary.Status == StatusFail {
		return errors.Errorf("doctor found %d failing check(s)", report.Summary.Failed)
	}

	// Skipped checks only warn, but an unreadable application means nothing was verified.
	if report.ApplicationClientID == "" {
		return errors.New("doctor could not inspect the application, so nothing about it was verified")
	}

	return nil
}

// connectDoctor authenticates, records the identity checks on the report, and
// returns a Graph client for the remaining inspection.
func connectDoctor(ctx context.Context, env cloudenv.Environment, report *DoctorReport) (*msgraphsdk.GraphServiceClient, error) {
	cred, err := authenticateToAzure(ctx, env, flagTenantID, flagVerbose)
	if err != nil {
		return nil, errors.Wrap(err, "authentication failed")
	}

	if err = validateAzureConnection(ctx, env, cred, flagVerbose); err != nil {
		return nil, errors.Wrap(err, "Azure connection validation failed")
	}

	client, err := newGraphClient(env, cred)
	if err != nil {
		return nil, err
	}

	// /me only exists for delegated flows, so app-only credentials fail here by design.
	directory, identityErr := describeSignedInUser(ctx, client)
	report.SignedInAs = directory.UserPrincipalName

	for _, check := range identityChecks(env, directory, identityErr) {
		report.Add(check)
	}

	if report.TenantID == "" {
		tenantID, tenantErr := getTenantID(ctx, client, flagVerbose)
		if tenantErr != nil {
			report.Add(CheckResult{
				Category: CategoryIdentity,
				Name:     "Tenant",
				Status:   StatusSkip,
				Summary:  "Could not read the tenant ID: " + tenantErr.Error(),
			})

			return client, nil
		}
		report.TenantID = tenantID
	}

	report.Add(CheckResult{
		Category: CategoryIdentity,
		Name:     "Tenant",
		Status:   StatusPass,
		Summary:  "Tenant ID " + report.TenantID,
	})

	return client, nil
}

// identityChecks describes who the tool is authenticating as. A missing user
// never degrades the verdict: nothing about the application depends on it.
func identityChecks(env cloudenv.Environment, directory directoryContext, identityErr error) []CheckResult {
	signIn := CheckResult{
		Category: CategoryIdentity,
		Name:     "Azure sign-in",
		Status:   StatusPass,
		Summary:  fmt.Sprintf("Acquired a Microsoft Graph token for the %s cloud as %s", env.Name, directory.UserPrincipalName),
		Details:  []string{"Graph endpoint: " + env.GraphBaseURL},
	}

	if identityErr == nil {
		return []CheckResult{signIn, checkDirectoryRoles(directory)}
	}

	signIn.Summary = fmt.Sprintf("Acquired a Microsoft Graph token for the %s cloud", env.Name)
	signIn.Details = append(signIn.Details,
		"no signed-in user: "+identityErr.Error(),
		"expected with application (client credentials) auth, which has no user context")

	return []CheckResult{signIn}
}

// checkDirectoryRoles reports whether the signed-in user can fix what the report finds.
func checkDirectoryRoles(directory directoryContext) CheckResult {
	result := CheckResult{Category: CategoryIdentity, Name: "Directory roles"}

	if directory.RolesErr != nil {
		result.Status = StatusSkip
		result.Summary = "Could not read directory roles: " + directory.RolesErr.Error()
		return result
	}

	if len(directory.AdminRoles) == 0 {
		result.Status = StatusWarn
		result.Summary = "The signed-in user holds no application administration role"
		result.Remediation = "Sign in as an Application Administrator, Cloud Application Administrator, or Global Administrator to apply fixes"
		return result
	}

	result.Status = StatusPass
	result.Summary = "Holds " + strings.Join(directory.AdminRoles, ", ")

	return result
}

// inspectApplication locates the application under inspection, gathers the
// related Graph objects, and appends every configuration check to the report.
func inspectApplication(ctx context.Context, client *msgraphsdk.GraphServiceClient, env cloudenv.Environment, report *DoctorReport, manifest *teamsManifest, manifestErr error) error {
	app, err := findApplicationForDoctor(ctx, client, flagAppName, flagClientID)
	if err != nil {
		// Recorded rather than returned so the identity checks and --report-file still get emitted.
		report.Add(CheckResult{
			Category:    CategoryApplication,
			Name:        "Application registration",
			Status:      StatusSkip,
			Summary:     "Could not search for the application: " + err.Error(),
			Remediation: "Grant the credential Application.Read.All, or see \"Permissions for the doctor\" in the README",
		})
		addManifestOnlyChecks(report, manifest, manifestErr)

		return nil
	}

	if app == nil {
		report.Add(CheckResult{
			Category:    CategoryApplication,
			Name:        "Application registration",
			Status:      StatusFail,
			Summary:     describeMissingApplication(flagAppName, flagClientID),
			Remediation: "Run `azure-setup create --site-url <mattermost-url>` to create the application",
		})
		addManifestOnlyChecks(report, manifest, manifestErr)

		return nil
	}

	report.ApplicationName = derefString(app.GetDisplayName())
	report.ApplicationClientID = derefString(app.GetAppId())
	report.ApplicationObjectID = derefString(app.GetId())
	if uris := app.GetIdentifierUris(); len(uris) > 0 {
		report.ApplicationIDURI = uris[0]
	}

	duplicates, duplicatesErr := findDuplicateApplications(ctx, client, report.ApplicationName, report.ApplicationClientID)

	matches := 1
	if flagClientID == "" {
		matches += len(duplicates)
	}
	report.Add(checkApplicationLookup(report, matches))

	inputs := doctorInputs{
		App:                app,
		SiteURL:            report.MattermostSiteURL,
		PortalHost:         env.PortalHost,
		Now:                time.Now(),
		SecretWarningDays:  flagSecretWarningDays,
		Manifest:           manifest,
		ManifestErr:        manifestErr,
		DuplicateClientIDs: duplicates,
		DuplicatesErr:      duplicatesErr,
	}

	if derefString(app.GetSignInAudience()) == AudienceMultipleOrgs {
		inputs.TenantRestriction, inputs.TenantRestrictionErr = readTenantRestriction(ctx, client, env, report.ApplicationObjectID)
	}

	graphSP, graphSPErr := findGraphServicePrincipal(ctx, client)
	inputs.GraphPermissionNames = graphPermissionNames(graphSP)

	inputs.ServicePrincipal, inputs.ServicePrincipalErr = findServicePrincipal(ctx, client, report.ApplicationClientID)
	inputs.Consent = readConsentState(ctx, client, inputs.ServicePrincipal, inputs.ServicePrincipalErr, graphSP, graphSPErr)
	inputs.Owners, inputs.OwnersErr = listApplicationOwners(ctx, client, report.ApplicationObjectID)

	// An empty id would issue `externalId eq ''` and can match an unrelated store app.
	if manifest != nil && manifest.ID != "" {
		inputs.CatalogLookup = true
		inputs.CatalogApp, inputs.CatalogErr = findCatalogApp(ctx, client, manifest.ID)
	}

	for _, check := range runDoctorChecks(inputs) {
		report.Add(check)
	}

	return nil
}

// checkApplicationLookup reports which registration the rest of the report
// describes. An ambiguous display-name match fails, because the lookup picks
// whichever registration Graph returns first.
func checkApplicationLookup(report *DoctorReport, matches int) CheckResult {
	result := CheckResult{Category: CategoryApplication, Name: "Application registration"}

	if matches > 1 {
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("%d applications are named %q; this report describes the one with object ID %s, which may not be the one in use",
			matches, report.ApplicationName, report.ApplicationObjectID)
		result.Remediation = "Re-run with --client-id to audit a specific registration"

		return result
	}

	result.Status = StatusPass
	result.Summary = fmt.Sprintf("Found %q (client ID %s)", report.ApplicationName, report.ApplicationClientID)

	return result
}

// loadDoctorManifest reads the manifest named by --manifest and records it on the report.
func loadDoctorManifest(report *DoctorReport) (*teamsManifest, error) {
	if flagManifest == "" {
		return nil, nil
	}

	manifest, err := loadManifest(flagManifest)
	if err != nil {
		return nil, err
	}

	report.ManifestPath = manifest.SourcePath

	// Shown as context only: deriving the expected URI from the manifest would be circular.
	if flagSiteURL == "" {
		report.ManifestHost = manifestResourceHost(manifest)
	}

	return manifest, nil
}

// addManifestOnlyChecks runs the manifest checks that need no registration data.
func addManifestOnlyChecks(report *DoctorReport, manifest *teamsManifest, manifestErr error) {
	if manifest == nil && manifestErr == nil {
		return
	}

	for _, check := range manifestChecks(doctorInputs{Manifest: manifest, ManifestErr: manifestErr}) {
		report.Add(check)
	}
}

// describeMissingApplication explains which lookup came up empty.
func describeMissingApplication(appName, clientID string) string {
	if clientID != "" {
		return "No application with client ID " + clientID + " exists in this tenant"
	}

	return fmt.Sprintf("No application named %q exists in this tenant", appName)
}

// findApplicationForDoctor looks up the application by client ID when one was
// supplied, otherwise by display name, selecting every property the checks read.
func findApplicationForDoctor(ctx context.Context, client *msgraphsdk.GraphServiceClient, appName, clientID string) (models.Applicationable, error) {
	filter := fmt.Sprintf("displayName eq '%s'", escapeODataString(appName))
	if clientID != "" {
		filter = fmt.Sprintf("appId eq '%s'", escapeODataString(clientID))
	}

	apps, err := client.Applications().Get(ctx, &applications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &applications.ApplicationsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: doctorApplicationSelect,
		},
	})
	if err != nil {
		return nil, err
	}

	if apps == nil || len(apps.GetValue()) == 0 {
		return nil, nil
	}

	return apps.GetValue()[0], nil
}

// findDuplicateApplications returns the client IDs of other applications that
// share the display name of the one being inspected.
func findDuplicateApplications(ctx context.Context, client *msgraphsdk.GraphServiceClient, appName, clientID string) ([]string, error) {
	filter := fmt.Sprintf("displayName eq '%s'", escapeODataString(appName))

	apps, err := client.Applications().Get(ctx, &applications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &applications.ApplicationsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: []string{"id", "appId", "displayName"},
		},
	})
	if err != nil {
		return nil, err
	}

	var duplicates []string
	if apps == nil {
		return duplicates, nil
	}

	err = iteratePages(ctx, client, apps,
		models.CreateApplicationCollectionResponseFromDiscriminatorValue,
		func(app models.Applicationable) {
			if otherID := derefString(app.GetAppId()); otherID != "" && !strings.EqualFold(otherID, clientID) {
				duplicates = append(duplicates, otherID)
			}
		})
	if err != nil {
		return nil, err
	}

	return duplicates, nil
}

// findServicePrincipal returns the service principal (enterprise application)
// backing the given client ID, or nil when none exists.
func findServicePrincipal(ctx context.Context, client *msgraphsdk.GraphServiceClient, clientID string) (models.ServicePrincipalable, error) {
	filter := fmt.Sprintf("appId eq '%s'", escapeODataString(clientID))

	principals, err := client.ServicePrincipals().Get(ctx, &serviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: &serviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: []string{"id", "appId", "displayName", "accountEnabled"},
		},
	})
	if err != nil {
		return nil, err
	}

	if principals == nil || len(principals.GetValue()) == 0 {
		return nil, nil
	}

	return principals.GetValue()[0], nil
}

// readConsentState collects the Microsoft Graph permissions granted to the
// application's service principal.
func readConsentState(ctx context.Context, client *msgraphsdk.GraphServiceClient, sp models.ServicePrincipalable, spErr error, graphSP models.ServicePrincipalable, graphSPErr error) consentState {
	if spErr != nil {
		return consentState{DelegatedErr: spErr, AppRoleErr: spErr}
	}

	if sp == nil {
		return consentState{DelegatedErr: errNoServicePrincipal, AppRoleErr: errNoServicePrincipal}
	}

	if graphSPErr != nil {
		return consentState{DelegatedErr: graphSPErr, AppRoleErr: graphSPErr}
	}

	graphSPID := derefString(graphSP.GetId())
	spID := derefString(sp.GetId())
	state := consentState{}

	grants, err := client.ServicePrincipals().ByServicePrincipalId(spID).Oauth2PermissionGrants().Get(ctx, nil)
	if err != nil {
		state.DelegatedErr = err
	} else {
		state.DelegatedErr = iteratePages(ctx, client, grants,
			models.CreateOAuth2PermissionGrantCollectionResponseFromDiscriminatorValue,
			func(grant models.OAuth2PermissionGrantable) {
				if !strings.EqualFold(derefString(grant.GetResourceId()), graphSPID) {
					return
				}

				scopes := strings.Fields(derefString(grant.GetScope()))
				if strings.EqualFold(derefString(grant.GetConsentType()), "AllPrincipals") {
					state.TenantWideScopes = append(state.TenantWideScopes, scopes...)
					return
				}

				state.UserScopes = append(state.UserScopes, scopes...)
			})
	}

	assignments, err := client.ServicePrincipals().ByServicePrincipalId(spID).AppRoleAssignments().Get(ctx, nil)
	if err != nil {
		state.AppRoleErr = err
	} else {
		state.AppRoleErr = iteratePages(ctx, client, assignments,
			models.CreateAppRoleAssignmentCollectionResponseFromDiscriminatorValue,
			func(assignment models.AppRoleAssignmentable) {
				if assignment.GetResourceId() == nil || !strings.EqualFold(assignment.GetResourceId().String(), graphSPID) {
					return
				}

				state.AppRoleIDs = append(state.AppRoleIDs, uuidString(assignment.GetAppRoleId()))
			})
	}

	return state
}

// readTenantRestriction reads the application's signInAudienceRestrictions,
// which only the beta endpoint exposes.
func readTenantRestriction(ctx context.Context, client *msgraphsdk.GraphServiceClient, env cloudenv.Environment, objectID string) (*tenantRestriction, error) {
	uri, err := url.Parse(strings.TrimSuffix(env.GraphBaseURL, "/v1.0") + "/beta/applications/" + url.PathEscape(objectID) + "?$select=signInAudienceRestrictions")
	if err != nil {
		return nil, err
	}

	request := abstractions.NewRequestInformation()
	request.Method = abstractions.GET
	request.SetUri(*uri)
	request.Headers.TryAdd("Accept", "application/json")

	body, err := client.GetAdapter().SendPrimitive(ctx, request, "[]byte", abstractions.ErrorMappings{
		"XXX": odataerrors.CreateODataErrorFromDiscriminatorValue,
	})
	if err != nil {
		return nil, err
	}

	raw, _ := body.([]byte)

	return parseTenantRestriction(raw)
}

// iteratePages walks every page of a Graph collection response, invoking visit
// for each item. A single Get does not follow @odata.nextLink.
func iteratePages[T any](
	ctx context.Context,
	client *msgraphsdk.GraphServiceClient,
	response any,
	factory serialization.ParsableFactory,
	visit func(item T),
) error {
	if response == nil {
		return nil
	}

	iterator, err := msgraphcore.NewPageIterator[T](response, client.GetAdapter(), factory)
	if err != nil {
		return err
	}

	return iterator.Iterate(ctx, func(item T) bool {
		visit(item)
		return true
	})
}

// findGraphServicePrincipal returns the Microsoft Graph service principal in this tenant.
func findGraphServicePrincipal(ctx context.Context, client *msgraphsdk.GraphServiceClient) (models.ServicePrincipalable, error) {
	filter := fmt.Sprintf("appId eq '%s'", GraphResourceID)

	principals, err := client.ServicePrincipals().Get(ctx, &serviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: &serviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: []string{"id", "appId", "appRoles", "oauth2PermissionScopes"},
		},
	})
	if err != nil {
		return nil, err
	}

	if principals == nil || len(principals.GetValue()) == 0 {
		return nil, errors.New("the Microsoft Graph service principal was not found in this tenant")
	}

	return principals.GetValue()[0], nil
}

// graphPermissionNames maps Graph permission IDs to their names, or returns nil
// when the Graph service principal could not be read.
func graphPermissionNames(graphSP models.ServicePrincipalable) map[string]string {
	if graphSP == nil {
		return nil
	}

	names := make(map[string]string)

	for _, role := range graphSP.GetAppRoles() {
		if role.GetValue() != nil && role.GetId() != nil {
			names[strings.ToLower(role.GetId().String())] = *role.GetValue()
		}
	}

	for _, scope := range graphSP.GetOauth2PermissionScopes() {
		if scope.GetValue() != nil && scope.GetId() != nil {
			names[strings.ToLower(scope.GetId().String())] = *scope.GetValue()
		}
	}

	return names
}

// listApplicationOwners returns a display name for each owner of the application.
func listApplicationOwners(ctx context.Context, client *msgraphsdk.GraphServiceClient, objectID string) ([]string, error) {
	owners, err := client.Applications().ByApplicationId(objectID).Owners().Get(ctx, nil)
	if err != nil {
		return nil, err
	}

	var names []string
	if owners == nil {
		return names, nil
	}

	err = iteratePages(ctx, client, owners,
		models.CreateDirectoryObjectCollectionResponseFromDiscriminatorValue,
		func(owner models.DirectoryObjectable) {
			names = append(names, describeDirectoryObject(owner))
		})
	if err != nil {
		return nil, err
	}

	return names, nil
}

// describeDirectoryObject renders the most identifiable label available for a
// directory object, falling back to its object ID.
func describeDirectoryObject(object models.DirectoryObjectable) string {
	switch typed := object.(type) {
	case models.Userable:
		if upn := derefString(typed.GetUserPrincipalName()); upn != "" {
			return upn
		}
		if name := derefString(typed.GetDisplayName()); name != "" {
			return name
		}
	case models.ServicePrincipalable:
		if name := derefString(typed.GetDisplayName()); name != "" {
			return name + " (service principal)"
		}
	}

	return derefString(object.GetId())
}

// emitDoctorReport writes the report to stdout and, when requested, to a file.
func emitDoctorReport(report *DoctorReport, format, reportFile string) error {
	if err := RenderDoctorReport(os.Stdout, report, format); err != nil {
		return err
	}

	if reportFile == "" {
		return nil
	}

	file, err := os.Create(reportFile) // #nosec G304
	if err != nil {
		return errors.Wrapf(err, "failed to create report file %s", reportFile)
	}

	if err = RenderDoctorReport(file, report, format); err != nil {
		_ = file.Close()
		return err
	}

	if err = file.Close(); err != nil {
		return errors.Wrapf(err, "failed to write report file %s", reportFile)
	}

	progressf("\n📄 Report written to %s\n", reportFile)

	return nil
}
