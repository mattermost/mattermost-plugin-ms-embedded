// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/pkg/errors"
)

// DefaultSecretWarningDays is how far ahead the doctor warns about a client
// secret that is about to expire.
const DefaultSecretWarningDays = 30

// errNoServicePrincipal marks the consent grants as unreadable because the
// application has no service principal to read them from.
var errNoServicePrincipal = errors.New("the application has no service principal")

// consentState holds the admin consent grants read from the application's
// service principal. The delegated grants and the application role assignments
// are read independently, so each carries its own error: failing to read one
// must not hide the other.
type consentState struct {
	// DelegatedErr records why the delegated grants could not be read, and is
	// nil once they have been read successfully.
	DelegatedErr error

	// TenantWideScopes are delegated scopes consented for every user in the
	// tenant (an AllPrincipals grant). Only these remove the consent prompt.
	TenantWideScopes []string

	// UserScopes are delegated scopes consented for individual users only.
	UserScopes []string

	// AppRoleErr records why the application role assignments could not be
	// read, and is nil once they have been read successfully.
	AppRoleErr error

	AppRoleIDs []string
}

// doctorInputs is everything the doctor reads from Microsoft Graph. Keeping the
// checks as pure functions over this struct means every rule is unit testable
// without a live tenant.
type doctorInputs struct {
	App        models.Applicationable
	SiteURL    string
	PortalHost string
	Now        time.Time

	SecretWarningDays int

	// GraphPermissionNames maps Microsoft Graph permission IDs to their names so
	// permissions the plugin does not require can still be reported by name.
	GraphPermissionNames map[string]string

	ServicePrincipal    models.ServicePrincipalable
	ServicePrincipalErr error

	Consent consentState

	Owners    []string
	OwnersErr error

	// DuplicateClientIDs lists other applications in the tenant that share the
	// display name of the application under inspection.
	DuplicateClientIDs []string
	DuplicatesErr      error
}

// runDoctorChecks evaluates every configuration rule against the data gathered
// from Azure and returns the results in report order.
func runDoctorChecks(in doctorInputs) []CheckResult {
	if in.SecretWarningDays <= 0 {
		in.SecretWarningDays = DefaultSecretWarningDays
	}

	return []CheckResult{
		checkSignInAudience(in.App),
		checkApplicationIDURI(in.App, in.SiteURL),
		checkExposedScope(in.App),
		checkPreAuthorizedClients(in.App),
		checkRequiredPermissions(in.App, in.GraphPermissionNames),
		checkServicePrincipal(in.ServicePrincipal, in.ServicePrincipalErr),
		checkDelegatedConsent(in.Consent, in.App, in.PortalHost),
		checkAppRoleConsent(in.Consent, in.App, in.PortalHost),
		checkClientSecrets(in.App, in.Now, in.SecretWarningDays),
		checkCertificates(in.App, in.Now),
		checkOwners(in.Owners, in.OwnersErr),
		checkDuplicateApplications(in.App, in.DuplicateClientIDs, in.DuplicatesErr),
	}
}

// checkSignInAudience verifies the application is registered as single tenant,
// which is what the plugin's token validation expects.
func checkSignInAudience(app models.Applicationable) CheckResult {
	const expected = "AzureADMyOrg"

	result := CheckResult{Category: CategoryApplication, Name: "Sign-in audience"}
	actual := derefString(app.GetSignInAudience())

	switch actual {
	case expected:
		result.Status = StatusPass
		result.Summary = "Single tenant (AzureADMyOrg)"
	case "":
		result.Status = StatusFail
		result.Summary = "Sign-in audience is not set"
		result.Remediation = "Re-run `azure-setup create` to set the sign-in audience to AzureADMyOrg"
	default:
		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("Sign-in audience is %q, the setup tool configures %q", actual, expected)
		result.Remediation = "Re-run `azure-setup create` unless the tenant intentionally uses a different audience"
	}

	return result
}

// checkApplicationIDURI verifies the identifier URI matches the one the Teams
// SSO token audience is validated against: api://<host><path>/<client-id>.
func checkApplicationIDURI(app models.Applicationable, siteURL string) CheckResult {
	result := CheckResult{Category: CategoryApplication, Name: "Application ID URI"}

	uris := app.GetIdentifierUris()
	clientID := derefString(app.GetAppId())

	for _, uri := range uris {
		result.Details = append(result.Details, "configured: "+uri)
	}

	if len(uris) == 0 {
		result.Status = StatusFail
		result.Summary = "No Application ID URI is set, so Teams SSO tokens cannot be issued"
		result.Remediation = "Re-run `azure-setup create --site-url <mattermost-url> --client-id " + clientID + "`"
		return result
	}

	if siteURL == "" {
		// Hosts are case-insensitive and Azure stores the URI exactly as it was
		// entered, so shape matching folds case.
		configured := strings.ToLower(uris[0])
		suffix := strings.ToLower("/" + clientID)

		if strings.HasPrefix(configured, "api://") && strings.HasSuffix(configured, suffix) {
			result.Status = StatusPass
			result.Summary = fmt.Sprintf("%s has the expected api://<host>/<client-id> shape", uris[0])
			result.Details = append(result.Details, "pass --site-url to verify the host matches the Mattermost server")
			return result
		}

		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("%s does not look like api://<host>/<client-id>", uris[0])
		result.Remediation = "Re-run the doctor with --site-url to verify the URI against the Mattermost server URL"
		return result
	}

	expected, err := buildApplicationIDURI(siteURL, clientID)
	if err != nil {
		result.Status = StatusSkip
		result.Summary = fmt.Sprintf("Could not derive the expected URI from %s: %v", siteURL, err)
		return result
	}

	// The expected URI is built from the operator-supplied --site-url, whose host
	// casing is preserved verbatim, so it is matched case-insensitively against
	// what Azure actually stores.
	matched := ""
	for _, uri := range uris {
		if strings.EqualFold(uri, expected) {
			matched = uri
			break
		}
	}

	if matched == "" {
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("Expected %s but the application does not have it", expected)
		result.Remediation = "Re-run `azure-setup create --site-url " + siteURL + " --client-id " + clientID + "`"
		return result
	}

	if len(uris) > 1 {
		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("%s is present, but %d identifier URIs are configured", matched, len(uris))
		result.Remediation = "Remove the unused identifier URIs in the Azure Portal to avoid ambiguous token audiences"
		return result
	}

	result.Status = StatusPass
	result.Summary = matched

	return result
}

// checkExposedScope verifies the access_as_user delegated scope exists and is
// usable: enabled, user-consentable, and described for the consent prompt.
func checkExposedScope(app models.Applicationable) CheckResult {
	result := CheckResult{Category: CategoryExposedAPI, Name: "Exposed scope (" + ScopeName + ")"}

	scope := findScopeByName(app, ScopeName)
	if scope == nil {
		result.Status = StatusFail
		result.Summary = "The " + ScopeName + " scope is not exposed"
		result.Remediation = "Re-run `azure-setup create` to expose the scope"
		return result
	}

	result.Details = append(result.Details, "scope ID: "+uuidString(scope.GetId()))

	if total := len(allScopeNames(app)); total > 1 {
		result.Details = append(result.Details, fmt.Sprintf("the application exposes %d scopes: %s", total, strings.Join(allScopeNames(app), ", ")))
	}

	// A disabled scope is never issued in a token, so SSO cannot work at all.
	if scope.GetIsEnabled() == nil || !*scope.GetIsEnabled() {
		result.Status = StatusFail
		result.Summary = "The scope exists but is disabled, so no token can be issued for it"
		result.Remediation = "Re-run `azure-setup create` to re-enable the scope"
		return result
	}

	var problems []string

	// An "Admin" scope still works for the pre-authorized Microsoft clients and
	// once admin consent is granted, which the consent checks cover separately.
	if scopeType := derefString(scope.GetTypeEscaped()); scopeType != "User" {
		problems = append(problems, fmt.Sprintf("consent type is %q, so users cannot consent for themselves", scopeType))
	}
	if derefString(scope.GetAdminConsentDisplayName()) == "" || derefString(scope.GetAdminConsentDescription()) == "" {
		problems = append(problems, "the admin consent display name or description is empty")
	}
	if derefString(scope.GetUserConsentDisplayName()) == "" || derefString(scope.GetUserConsentDescription()) == "" {
		problems = append(problems, "the user consent display name or description is empty")
	}

	if len(problems) > 0 {
		result.Status = StatusWarn
		result.Summary = "The scope is enabled but " + strings.Join(problems, "; ")
		result.Remediation = "Re-run `azure-setup create` to restore the expected scope definition"
		return result
	}

	result.Status = StatusPass
	result.Summary = "Exposed, enabled, and consentable by users"

	return result
}

// checkPreAuthorizedClients verifies every Microsoft first-party client the
// plugin embeds into is pre-authorized for the access_as_user scope, which is
// what removes the consent prompt inside Teams, Outlook, and Office.
func checkPreAuthorizedClients(app models.Applicationable) CheckResult {
	result := CheckResult{Category: CategoryExposedAPI, Name: "Pre-authorized Microsoft clients"}

	scopeID := findExistingScopeID(app)
	if scopeID == uuid.Nil {
		result.Status = StatusSkip
		result.Summary = "Skipped because the " + ScopeName + " scope does not exist yet"
		return result
	}

	// Azure preserves the casing a client ID was entered with, so every
	// comparison here is case-insensitive.
	managed := make(map[string]bool, len(getPreAuthorizedClients()))
	for _, clientID := range getPreAuthorizedClients() {
		managed[strings.ToLower(clientID)] = true
	}

	granted := make(map[string]bool)
	var extra []string

	if app.GetApi() != nil {
		for _, preAuth := range app.GetApi().GetPreAuthorizedApplications() {
			clientID := derefString(preAuth.GetAppId())
			if clientID == "" {
				continue
			}

			if containsFold(preAuth.GetDelegatedPermissionIds(), scopeID.String()) {
				granted[strings.ToLower(clientID)] = true
			}

			if !managed[strings.ToLower(clientID)] {
				extra = append(extra, clientID)
			}
		}
	}

	var missing []string
	for _, clientID := range getPreAuthorizedClients() {
		if !granted[strings.ToLower(clientID)] {
			missing = append(missing, clientID)
		}
	}

	for _, clientID := range extra {
		result.Details = append(result.Details, "additional pre-authorized client (not managed by this tool): "+clientID)
	}

	if len(missing) > 0 {
		for _, clientID := range missing {
			result.Details = append(result.Details, "missing: "+clientID+" ("+describePreAuthorizedClient(clientID)+")")
		}

		result.Status = StatusFail
		result.Summary = fmt.Sprintf("%d of %d Microsoft clients are not pre-authorized for %s, so SSO will prompt for consent there",
			len(missing), len(getPreAuthorizedClients()), ScopeName)
		result.Remediation = "Re-run `azure-setup create --client-id " + derefString(app.GetAppId()) + "` to re-apply the pre-authorized client list"

		return result
	}

	result.Status = StatusPass
	result.Summary = fmt.Sprintf("All %d Microsoft clients are pre-authorized for %s", len(getPreAuthorizedClients()), ScopeName)

	return result
}

// checkRequiredPermissions verifies only the permissions the plugin itself
// needs. Anything else on the application - including the read-only permissions
// added by `create --create-doctor-requirements` - is named in the details but
// never affects the status: this tool does not require them to be present.
func checkRequiredPermissions(app models.Applicationable, permissionNames map[string]string) CheckResult {
	result := CheckResult{Category: CategoryPermissions, Name: "Requested Graph permissions"}

	type permissionKey struct {
		id       string
		permType string
	}

	configured := make(map[permissionKey]bool)
	var extra []string

	for _, resource := range app.GetRequiredResourceAccess() {
		resourceAppID := derefString(resource.GetResourceAppId())

		for _, access := range resource.GetResourceAccess() {
			id := uuidString(access.GetId())
			accessType := derefString(access.GetTypeEscaped())

			if !strings.EqualFold(resourceAppID, GraphResourceID) {
				extra = append(extra, fmt.Sprintf("non-Graph permission on resource %s: %s (%s)", resourceAppID, id, permissionKindLabel(accessType)))
				continue
			}

			configured[permissionKey{id: id, permType: accessType}] = true

			if isRequiredPermission(id, accessType) {
				continue
			}

			name := describeGraphPermission(permissionNames, id)
			if slices.Contains(doctorRequirementNames, name) {
				extra = append(extra, fmt.Sprintf("%s (%s): used by the doctor command, not required by the plugin", name, permissionKindLabel(accessType)))
				continue
			}

			extra = append(extra, fmt.Sprintf("additional Graph permission not managed by this tool: %s (%s)", name, permissionKindLabel(accessType)))
		}
	}

	var missing []string
	for _, perm := range getRequiredPermissions() {
		if configured[permissionKey{id: perm.ResourceID, permType: perm.Type}] {
			result.Details = append(result.Details, fmt.Sprintf("%s (%s): requested", perm.Name, permissionKindLabel(perm.Type)))
			continue
		}
		missing = append(missing, fmt.Sprintf("%s (%s)", perm.Name, permissionKindLabel(perm.Type)))
	}

	result.Details = append(result.Details, extra...)

	if len(missing) > 0 {
		result.Status = StatusFail
		result.Summary = "Missing required permissions: " + strings.Join(missing, ", ")
		result.Remediation = "Re-run `azure-setup create --client-id " + derefString(app.GetAppId()) + "` to request the missing permissions"
		return result
	}

	result.Status = StatusPass
	result.Summary = fmt.Sprintf("All %d required Graph permissions are requested", len(getRequiredPermissions()))

	return result
}

// checkServicePrincipal verifies the application has an enabled service
// principal in the tenant, which is required before admin consent can be given.
func checkServicePrincipal(sp models.ServicePrincipalable, lookupErr error) CheckResult {
	result := CheckResult{Category: CategoryConsent, Name: "Service principal"}

	if lookupErr != nil {
		result.Status = StatusSkip
		result.Summary = "Could not read service principals: " + lookupErr.Error()
		return result
	}

	if sp == nil {
		result.Status = StatusFail
		result.Summary = "No service principal (enterprise application) exists for this application"
		result.Remediation = "Re-run `azure-setup create` or create the enterprise application in the Azure Portal"
		return result
	}

	result.Details = append(result.Details, "object ID: "+derefString(sp.GetId()))

	if sp.GetAccountEnabled() != nil && !*sp.GetAccountEnabled() {
		result.Status = StatusFail
		result.Summary = "The service principal exists but is disabled, so sign-in is blocked"
		result.Remediation = "Enable the enterprise application in Azure Portal > Enterprise applications > Properties"
		return result
	}

	result.Status = StatusPass
	result.Summary = "Exists and is enabled"

	return result
}

// checkDelegatedConsent verifies the delegated Graph permissions have been
// consented for the whole tenant.
func checkDelegatedConsent(consent consentState, app models.Applicationable, portalHost string) CheckResult {
	result := CheckResult{Category: CategoryConsent, Name: "Admin consent (delegated permissions)"}

	if consent.DelegatedErr != nil {
		result.Status = StatusSkip
		result.Summary = skipConsentSummary(consent.DelegatedErr)
		return result
	}

	required := requiredPermissionNames(PermissionTypeScope)

	// A scope consented per user still prompts everyone else, so a tenant-wide
	// grant and a single-user grant are tracked separately rather than merged.
	var missing, userOnly []string
	for _, name := range required {
		switch {
		case containsFold(consent.TenantWideScopes, name):
			result.Details = append(result.Details, name+": granted tenant-wide")
		case containsFold(consent.UserScopes, name):
			userOnly = append(userOnly, name)
			result.Details = append(result.Details, name+": granted for individual users only")
		default:
			missing = append(missing, name)
		}
	}

	if len(consent.TenantWideScopes) > 0 {
		result.Details = append(result.Details, "all tenant-wide scopes: "+strings.Join(consent.TenantWideScopes, ", "))
	}

	consentLink := adminConsentURL(portalHost, derefString(app.GetAppId()))

	if len(missing) > 0 {
		// The plugin authenticates to Graph app-only (server/plugin.go builds
		// the app client, which uses a client-credentials token), so it never
		// exercises a delegated permission. A tenant that consented only the
		// application permissions is therefore a working install, and failing
		// the run would turn a healthy deployment red in CI. It is still
		// reported, because `azure-setup create` requests these and an
		// operator who believes they were consented should know otherwise.
		result.Status = StatusWarn
		result.Summary = "Not consented: " + strings.Join(missing, ", ") +
			" (the plugin authenticates app-only, so this does not block it)"
		result.Remediation = "Grant admin consent at " + consentLink
		return result
	}

	if len(userOnly) > 0 {
		result.Status = StatusWarn
		result.Summary = "Consented for individual users only, so everyone else is still prompted: " + strings.Join(userOnly, ", ")
		result.Remediation = "Grant admin consent for the whole tenant at " + consentLink
		return result
	}

	result.Status = StatusPass
	result.Summary = "Granted tenant-wide for " + strings.Join(required, ", ")

	return result
}

// checkAppRoleConsent verifies the application (app-only) Graph permissions have
// been granted to the service principal.
func checkAppRoleConsent(consent consentState, app models.Applicationable, portalHost string) CheckResult {
	result := CheckResult{Category: CategoryConsent, Name: "Admin consent (application permissions)"}

	if consent.AppRoleErr != nil {
		result.Status = StatusSkip
		result.Summary = skipConsentSummary(consent.AppRoleErr)
		return result
	}

	var missing []string
	for _, perm := range getRequiredPermissions() {
		if perm.Type != PermissionTypeRole {
			continue
		}
		if containsFold(consent.AppRoleIDs, perm.ResourceID) {
			result.Details = append(result.Details, perm.Name+": granted")
			continue
		}
		missing = append(missing, perm.Name)
	}

	if len(missing) > 0 {
		result.Status = StatusFail
		result.Summary = "Not consented: " + strings.Join(missing, ", ")
		result.Remediation = "Grant admin consent at " + adminConsentURL(portalHost, derefString(app.GetAppId()))
		return result
	}

	result.Status = StatusPass
	result.Summary = "Granted for " + strings.Join(requiredPermissionNames(PermissionTypeRole), ", ")

	return result
}

// checkClientSecrets reports on the application's client secrets: the plugin
// needs at least one that has not expired, and operators need warning before
// the one in use lapses.
func checkClientSecrets(app models.Applicationable, now time.Time, warningDays int) CheckResult {
	result := CheckResult{Category: CategoryCredentials, Name: "Client secrets"}

	credentials := app.GetPasswordCredentials()
	if len(credentials) == 0 {
		result.Status = StatusFail
		result.Summary = "The application has no client secret"
		result.Remediation = "Run `azure-setup create --client-id " + derefString(app.GetAppId()) + "` to generate a new secret"
		return result
	}

	warnThreshold := now.AddDate(0, 0, warningDays)

	var valid, expiring, expired int
	var soonest time.Time

	for _, credential := range credentials {
		name := derefString(credential.GetDisplayName())
		if name == "" {
			name = "(unnamed)"
		}

		expiry := credential.GetEndDateTime()
		if expiry == nil {
			valid++
			result.Details = append(result.Details, name+": no expiration date reported")
			continue
		}

		switch {
		case expiry.Before(now):
			expired++
			result.Details = append(result.Details, fmt.Sprintf("%s: expired on %s", name, expiry.Format("2006-01-02")))
		case expiry.Before(warnThreshold):
			valid++
			expiring++
			result.Details = append(result.Details, fmt.Sprintf("%s: expires on %s (in %d day(s))", name, expiry.Format("2006-01-02"), daysUntil(now, *expiry)))
		default:
			valid++
			result.Details = append(result.Details, fmt.Sprintf("%s: valid until %s", name, expiry.Format("2006-01-02")))
		}

		if !expiry.Before(now) && (soonest.IsZero() || expiry.Before(soonest)) {
			soonest = *expiry
		}
	}

	switch {
	case valid == 0:
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("All %d client secret(s) have expired", expired)
		result.Remediation = "Run `azure-setup create --client-id " + derefString(app.GetAppId()) + "` to generate a new secret and update the plugin settings"
	case expiring == valid:
		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("Every valid secret expires within %d day(s) (next on %s)", warningDays, soonest.Format("2006-01-02"))
		result.Remediation = "Rotate the client secret before it expires to avoid an outage"
	default:
		result.Status = StatusPass
		result.Summary = fmt.Sprintf("%d valid secret(s); the longest-lived one is safe for more than %d day(s)", valid, warningDays)
	}

	if expired > 0 && valid > 0 {
		result.Details = append(result.Details, fmt.Sprintf("%d expired secret(s) are still attached and can be removed", expired))
	}

	return result
}

// checkCertificates reports certificate credentials. The plugin authenticates
// with a client secret, so certificates are informational only.
func checkCertificates(app models.Applicationable, now time.Time) CheckResult {
	result := CheckResult{Category: CategoryCredentials, Name: "Certificates", Status: StatusPass}

	credentials := app.GetKeyCredentials()
	if len(credentials) == 0 {
		result.Summary = "None configured (the plugin authenticates with a client secret)"
		return result
	}

	expired := 0
	for _, credential := range credentials {
		name := derefString(credential.GetDisplayName())
		if name == "" {
			name = "(unnamed)"
		}

		expiry := credential.GetEndDateTime()
		if expiry == nil {
			result.Details = append(result.Details, name+": no expiration date reported")
			continue
		}

		if expiry.Before(now) {
			expired++
			result.Details = append(result.Details, fmt.Sprintf("%s: expired on %s", name, expiry.Format("2006-01-02")))
			continue
		}

		result.Details = append(result.Details, fmt.Sprintf("%s: valid until %s", name, expiry.Format("2006-01-02")))
	}

	result.Summary = fmt.Sprintf("%d certificate(s) configured; not used by the plugin", len(credentials))

	if expired > 0 {
		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("%d of %d certificate(s) have expired", expired, len(credentials))
		result.Remediation = "Remove the expired certificates in the Azure Portal, or ignore this if they belong to another integration"
	}

	return result
}

// checkOwners flags an application with no owners, which can only be managed by
// tenant-wide administrators and is easy to orphan.
func checkOwners(owners []string, lookupErr error) CheckResult {
	result := CheckResult{Category: CategoryHousekeeping, Name: "Application owners"}

	if lookupErr != nil {
		result.Status = StatusSkip
		result.Summary = "Could not read the owner list: " + lookupErr.Error()
		return result
	}

	if len(owners) == 0 {
		result.Status = StatusWarn
		result.Summary = "The application has no owners"
		result.Remediation = "Assign at least one owner in Azure Portal > App registrations > Owners so it does not become unmanaged"
		return result
	}

	result.Status = StatusPass
	result.Summary = fmt.Sprintf("%d owner(s)", len(owners))
	result.Details = owners

	return result
}

// checkDuplicateApplications flags other registrations sharing the display name,
// which is the usual cause of the setup tool updating the wrong application.
func checkDuplicateApplications(app models.Applicationable, duplicates []string, lookupErr error) CheckResult {
	result := CheckResult{Category: CategoryHousekeeping, Name: "Duplicate registrations"}

	if lookupErr != nil {
		result.Status = StatusSkip
		result.Summary = "Could not search for applications with the same name: " + lookupErr.Error()
		return result
	}

	if len(duplicates) == 0 {
		result.Status = StatusPass
		result.Summary = fmt.Sprintf("%q is unique in this tenant", derefString(app.GetDisplayName()))
		return result
	}

	for _, clientID := range duplicates {
		result.Details = append(result.Details, "also registered as client ID "+clientID)
	}

	result.Status = StatusWarn
	result.Summary = fmt.Sprintf("%d other application(s) share the name %q", len(duplicates), derefString(app.GetDisplayName()))
	result.Remediation = "Always pass --client-id to `azure-setup` so it targets this registration, and delete the unused duplicates"

	return result
}

// findScopeByName returns the exposed OAuth2 permission scope with the given
// value, or nil when the application does not expose it.
func findScopeByName(app models.Applicationable, name string) models.PermissionScopeable {
	if app.GetApi() == nil {
		return nil
	}

	for _, scope := range app.GetApi().GetOauth2PermissionScopes() {
		if derefString(scope.GetValue()) == name {
			return scope
		}
	}

	return nil
}

// allScopeNames returns the values of every scope the application exposes.
func allScopeNames(app models.Applicationable) []string {
	if app.GetApi() == nil {
		return nil
	}

	var names []string
	for _, scope := range app.GetApi().GetOauth2PermissionScopes() {
		names = append(names, derefString(scope.GetValue()))
	}

	return names
}

// isRequiredPermission reports whether the Graph permission ID and type pair is
// one this tool configures.
func isRequiredPermission(id, permType string) bool {
	for _, perm := range getRequiredPermissions() {
		if strings.EqualFold(perm.ResourceID, id) && perm.Type == permType {
			return true
		}
	}

	return false
}

// requiredPermissionNames returns the names of the required permissions of the
// given type ("Scope" for delegated, "Role" for application).
func requiredPermissionNames(permType string) []string {
	var names []string
	for _, perm := range getRequiredPermissions() {
		if perm.Type == permType {
			names = append(names, perm.Name)
		}
	}

	return names
}

// describeGraphPermission renders a Graph permission ID as its name when the
// name could be resolved from the Graph service principal, and as the raw ID
// otherwise.
func describeGraphPermission(names map[string]string, id string) string {
	if name, ok := names[strings.ToLower(id)]; ok {
		return name
	}

	return id
}

// permissionKindLabel renders a Graph permission type the way the Azure Portal does.
func permissionKindLabel(permType string) string {
	if permType == PermissionTypeRole {
		return "Application"
	}

	return "Delegated"
}

// describePreAuthorizedClient maps a Microsoft first-party client ID to a
// human-readable name so a missing entry says which product breaks.
func describePreAuthorizedClient(clientID string) string {
	switch clientID {
	case ClientIDTeamsWeb:
		return "Microsoft Teams web"
	case ClientIDTeamsMobileDesktop:
		return "Microsoft Teams desktop and mobile"
	case ClientIDOutlookWeb:
		return "Microsoft Outlook web"
	case ClientIDOutlookDesktop:
		return "Microsoft Outlook desktop and Office mobile"
	case ClientIDOutlookMobile:
		return "Microsoft Outlook mobile"
	case ClientIDOfficeWeb:
		return "Microsoft 365 web"
	case ClientIDOfficeDesktop:
		return "Microsoft 365 desktop"
	case ClientIDCopilot:
		return "Microsoft 365 Copilot"
	case ClientIDOfficeUniversal:
		return "Microsoft Office universal endpoints"
	default:
		return "unknown Microsoft client"
	}
}

// skipConsentSummary explains why a consent check could not run.
func skipConsentSummary(err error) string {
	if errors.Is(err, errNoServicePrincipal) {
		return "Skipped because the application has no service principal to hold the grants"
	}

	return "Could not read the consent grants: " + err.Error()
}

// containsFold reports whether values contains target, ignoring case.
func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}

	return false
}

// daysUntil returns the whole days between now and the given time, rounded up so
// an expiry later today reads as 1 day rather than 0.
func daysUntil(now, future time.Time) int {
	remaining := future.Sub(now)
	if remaining <= 0 {
		return 0
	}

	days := int(remaining.Hours() / 24)
	if remaining%(24*time.Hour) != 0 {
		days++
	}

	return days
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func uuidString(value *uuid.UUID) string {
	if value == nil {
		return ""
	}

	return value.String()
}
