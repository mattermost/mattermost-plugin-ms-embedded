// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/microsoft/kiota-abstractions-go/serialization"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/cloudenv"
)

// doctorApplicationSelect lists every application property the doctor inspects.
// Graph omits most of these unless they are explicitly selected.
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
	// Flags have parsed by the time RunE is reached, so any failure from here on
	// is a finding or an Azure error rather than a usage mistake: the report must
	// not be followed by a dump of the flag list.
	cmd.SilenceUsage = true

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env, err := resolveCloud(flagCloud)
	if err != nil {
		return errors.Wrap(err, "invalid input")
	}

	// Validate every flag before authenticating: an interactive login plus a
	// full Graph inspection is an expensive way to learn about a typo.
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

	// Loaded before authenticating so a malformed package fails fast, and
	// because it can supply the site URL the Application ID URI is checked
	// against. It deliberately never supplies the client ID: both sides of that
	// comparison coming from the same file would make the check vacuous.
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

	// Individual skipped checks only warn, because a deliberately narrow
	// credential makes some of them unanswerable by design. Failing to read
	// the application at all is different: nothing about it was verified, so
	// exiting 0 would tell a CI job the opposite of the truth.
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

	// The signed-in identity is looked up with /me, which only exists for
	// delegated flows. App-only credentials (the usual CI setup) cannot call it,
	// so a failure here degrades the identity checks instead of ending the run.
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

// identityChecks describes who the tool is authenticating as.
//
// The signed-in identity is read with /me, which only exists for delegated
// flows. Nothing in the audit depends on it: the doctor never writes, so the
// operator's own roles do not affect what it can determine about the
// application, and application credentials are the documented way to run this
// command. Its absence is therefore reported as context and never degrades the
// verdict - a skip here would imply something about the application went
// unverified, and make a clean audit permanently unable to report pass.
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

	// With no user, "which roles does the user hold" is not applicable rather
	// than unanswered, so the check is omitted instead of skipped.
	return []CheckResult{signIn}
}

// checkDirectoryRoles turns the signed-in user's directory roles into a check so
// the report explains up front whether the operator can fix what it finds. It
// is only meaningful for a delegated sign-in; callers omit it entirely when
// there is no user.
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
	app, matches, err := findApplicationForDoctor(ctx, client, flagAppName, flagClientID)
	if err != nil {
		// Reading the application is the one lookup every later check depends
		// on, so the inspection stops here - but the report is still finalized
		// and emitted. A credential that cannot read applications is precisely
		// what the operator needs told, and returning an error instead would
		// discard the identity checks that already passed and skip
		// --report-file entirely.
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

	report.Add(checkApplicationLookup(report, matches))

	inputs := doctorInputs{
		App:               app,
		SiteURL:           report.MattermostSiteURL,
		PortalHost:        env.PortalHost,
		Now:               time.Now(),
		SecretWarningDays: flagSecretWarningDays,
		Manifest:          manifest,
		ManifestErr:       manifestErr,
	}

	// The Microsoft Graph service principal is read once: it is the resource
	// every consent grant points at, and it also names the permission IDs found
	// on the application.
	graphSP, graphSPErr := findGraphServicePrincipal(ctx, client)
	inputs.GraphPermissionNames = graphPermissionNames(graphSP)

	inputs.ServicePrincipal, inputs.ServicePrincipalErr = findServicePrincipal(ctx, client, report.ApplicationClientID)
	inputs.Consent = readConsentState(ctx, client, inputs.ServicePrincipal, inputs.ServicePrincipalErr, graphSP, graphSPErr)
	inputs.Owners, inputs.OwnersErr = listApplicationOwners(ctx, client, report.ApplicationObjectID)
	inputs.DuplicateClientIDs, inputs.DuplicatesErr = findDuplicateApplications(ctx, client, report.ApplicationName, report.ApplicationClientID)

	// The catalog lookup needs the manifest's id, so it only runs when a
	// manifest was supplied and parsed.
	// An empty id would issue `externalId eq ''`, which can match an unrelated
	// store-distributed app and report it as this one. The Teams app ID check
	// already fails for that case.
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
// describes. Several registrations can share a display name, and the lookup
// picks the first one Graph returns, so an ambiguous match is a failure: every
// check below it would otherwise be a confident verdict about an arbitrary app.
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

// loadDoctorManifest reads the manifest named by --manifest and records it on
// the report. When --site-url was not given, the host the manifest itself
// claims is used instead, so a package alone is enough to verify the
// Application ID URI.
func loadDoctorManifest(report *DoctorReport) (*teamsManifest, error) {
	if flagManifest == "" {
		return nil, nil
	}

	manifest, err := loadManifest(flagManifest)
	if err != nil {
		return nil, err
	}

	report.ManifestPath = manifest.SourcePath

	// The site URL is deliberately NOT taken from the manifest. Building the
	// expected Application ID URI out of the manifest and then comparing it to
	// the registration would be circular: checkApplicationIDURI would pass even
	// when both sides name the wrong server. The manifest is cross-checked
	// against the registration directly instead, which is a real comparison
	// between two independent sources.
	if flagSiteURL == "" {
		report.ManifestHost = manifestResourceHost(manifest)
	}

	return manifest, nil
}

// addManifestOnlyChecks runs the manifest checks that need no registration
// data, for the paths where the application could not be read. Most of the
// manifest is self-consistent and worth reporting on regardless - including
// the manifest being unreadable at all.
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
// The second return value is how many applications matched, so the caller can
// flag an ambiguous display-name lookup.
func findApplicationForDoctor(ctx context.Context, client *msgraphsdk.GraphServiceClient, appName, clientID string) (models.Applicationable, int, error) {
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
		return nil, 0, err
	}

	if apps == nil || len(apps.GetValue()) == 0 {
		return nil, 0, nil
	}

	// The match count decides whether the report is describing an unambiguous
	// registration, so it has to span every page rather than the first one.
	matches := 0
	if err = iteratePages(ctx, client, apps,
		models.CreateApplicationCollectionResponseFromDiscriminatorValue,
		func(models.Applicationable) { matches++ },
	); err != nil {
		return nil, 0, err
	}

	return apps.GetValue()[0], matches, nil
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

// readConsentState collects the Graph permissions actually granted to the
// application's service principal. Both delegated grants and application role
// assignments are scoped to the Microsoft Graph service principal so unrelated
// grants in the tenant are ignored, and each lookup records its own error so a
// failure to read one does not hide the other.
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

	// Both collections are paged. Per-user consent creates one grant per user,
	// so a widely used application can hold far more than a single page, and a
	// truncated read would report consent that exists as missing.
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

				// Each grant carries its own consent type, so the scopes are kept
				// apart: merging them would report a single user's consent as
				// tenant-wide whenever any other grant happens to be AllPrincipals.
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

// iteratePages walks every page of a Graph collection response, invoking visit
// for each item. Graph caps a collection page at a few hundred items and
// signals the rest with an @odata.nextLink, which a single Get does not follow.
func iteratePages[T any](
	ctx context.Context,
	client *msgraphsdk.GraphServiceClient,
	response any,
	factory serialization.ParsableFactory,
	visit func(item T),
) error {
	// A nil collection means Graph returned no body: an empty result, not a
	// failure, so it must not surface as an unreadable check.
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

// findGraphServicePrincipal returns the Microsoft Graph service principal in
// this tenant. It is the resource every plugin permission is granted against,
// and its appRoles and oauth2PermissionScopes name the permission IDs that
// appear on the application.
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

// graphPermissionNames maps Graph permission IDs to their names, so the report
// can print "Directory.Read.All" instead of a bare UUID. It returns nil when the
// Graph service principal could not be read, which callers treat as "unknown".
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

	// The path comes from the operator's own --report-file flag, so it is
	// deliberately arbitrary.
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
