// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testClientID = "11111111-2222-3333-4444-555555555555"
	testObjectID = "99999999-8888-7777-6666-555555555555"
	testSiteURL  = "https://mattermost.example.com"
)

// testScopeID is the access_as_user scope ID used by the fixtures below.
var testScopeID = uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

// healthyApp returns an application configured exactly the way `azure-setup
// create` leaves it. Tests degrade a copy of it to exercise the failure paths.
func healthyApp(t *testing.T) models.Applicationable {
	t.Helper()

	app := models.NewApplication()
	app.SetId(ptr(testObjectID))
	app.SetAppId(ptr(testClientID))
	app.SetDisplayName(ptr("Mattermost for Teams"))
	app.SetSignInAudience(ptr("AzureADMyOrg"))

	uri, err := buildApplicationIDURI(testSiteURL, testClientID)
	require.NoError(t, err)
	app.SetIdentifierUris([]string{uri})

	app.SetApi(healthyAPI(testScopeID))
	app.SetRequiredResourceAccess(healthyResourceAccess(t))
	app.SetPasswordCredentials([]models.PasswordCredentialable{
		passwordCredential("Mattermost Plugin Secret", time.Now().AddDate(0, 6, 0)),
	})

	return app
}

func healthyAPI(scopeID uuid.UUID) models.ApiApplicationable {
	scope := models.NewPermissionScope()
	scope.SetId(&scopeID)
	scope.SetValue(ptr(ScopeName))
	scope.SetIsEnabled(ptr(true))
	scope.SetTypeEscaped(ptr("User"))
	scope.SetAdminConsentDisplayName(ptr("Access Mattermost"))
	scope.SetAdminConsentDescription(ptr(ScopeDescription))
	scope.SetUserConsentDisplayName(ptr("Access Mattermost"))
	scope.SetUserConsentDescription(ptr("Allow the app to access Mattermost on your behalf"))

	api := models.NewApiApplication()
	api.SetOauth2PermissionScopes([]models.PermissionScopeable{scope})

	var preAuthorized []models.PreAuthorizedApplicationable
	for _, clientID := range getPreAuthorizedClients() {
		preAuth := models.NewPreAuthorizedApplication()
		preAuth.SetAppId(ptr(clientID))
		preAuth.SetDelegatedPermissionIds([]string{scopeID.String()})
		preAuthorized = append(preAuthorized, preAuth)
	}
	api.SetPreAuthorizedApplications(preAuthorized)

	return api
}

func healthyResourceAccess(t *testing.T) []models.RequiredResourceAccessable {
	t.Helper()

	access, err := buildRequiredResourceAccess(getRequiredPermissions())
	require.NoError(t, err)

	return access
}

func passwordCredential(name string, expiry time.Time) models.PasswordCredentialable {
	credential := models.NewPasswordCredential()
	credential.SetDisplayName(&name)
	credential.SetEndDateTime(&expiry)

	return credential
}

func ptr[T any](value T) *T {
	return new(value)
}

// healthyConsent returns a consent state where every required permission has
// been granted tenant-wide.
func healthyConsent() consentState {
	var roleIDs []string
	for _, perm := range getRequiredPermissions() {
		if perm.Type == PermissionTypeRole {
			roleIDs = append(roleIDs, perm.ResourceID)
		}
	}

	return consentState{
		TenantWideScopes: []string{"User.Read", "profile", "openid"},
		AppRoleIDs:       roleIDs,
	}
}

func healthyInputs(t *testing.T) doctorInputs {
	t.Helper()

	sp := models.NewServicePrincipal()
	sp.SetId(ptr("sp-object-id"))
	sp.SetAppId(ptr(testClientID))
	sp.SetAccountEnabled(ptr(true))

	return doctorInputs{
		App:               healthyApp(t),
		SiteURL:           testSiteURL,
		PortalHost:        "portal.azure.com",
		Now:               time.Now(),
		SecretWarningDays: DefaultSecretWarningDays,
		ServicePrincipal:  sp,
		Consent:           healthyConsent(),
		Owners:            []string{"admin@example.com"},
	}
}

// statusOfCheck returns the status of the named check from a full run.
func statusOfCheck(t *testing.T, checks []CheckResult, name string) CheckStatus {
	t.Helper()

	for _, check := range checks {
		if check.Name == name {
			return check.Status
		}
	}

	t.Fatalf("check %q not found in %d results", name, len(checks))

	return ""
}

func TestRunDoctorChecksHealthyApplication(t *testing.T) {
	checks := runDoctorChecks(healthyInputs(t))

	for _, check := range checks {
		assert.Equalf(t, StatusPass, check.Status, "check %q should pass: %s", check.Name, check.Summary)
		assert.NotEmptyf(t, check.Category, "check %q must have a category", check.Name)
		assert.NotEmptyf(t, check.Summary, "check %q must have a summary", check.Name)
	}
}

func TestCheckSignInAudience(t *testing.T) {
	tests := []struct {
		name     string
		audience *string
		expected CheckStatus
	}{
		{name: "single tenant", audience: ptr("AzureADMyOrg"), expected: StatusPass},
		{name: "multi tenant", audience: ptr("AzureADMultipleOrgs"), expected: StatusWarn},
		{name: "unset", audience: nil, expected: StatusFail},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := healthyApp(t)
			app.SetSignInAudience(test.audience)

			assert.Equal(t, test.expected, checkSignInAudience(app).Status)
		})
	}
}

func TestCheckApplicationIDURI(t *testing.T) {
	t.Run("matches the site URL", func(t *testing.T) {
		result := checkApplicationIDURI(healthyApp(t), testSiteURL)
		assert.Equal(t, StatusPass, result.Status)
		assert.Equal(t, "api://mattermost.example.com/"+testClientID, result.Summary)
	})

	t.Run("missing", func(t *testing.T) {
		app := healthyApp(t)
		app.SetIdentifierUris(nil)

		result := checkApplicationIDURI(app, testSiteURL)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Remediation, testClientID)
	})

	t.Run("points at a different host", func(t *testing.T) {
		app := healthyApp(t)
		app.SetIdentifierUris([]string{"api://other.example.com/" + testClientID})

		result := checkApplicationIDURI(app, testSiteURL)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "api://mattermost.example.com/"+testClientID)
	})

	t.Run("several URIs are ambiguous", func(t *testing.T) {
		app := healthyApp(t)
		app.SetIdentifierUris([]string{
			"api://mattermost.example.com/" + testClientID,
			"api://stale.example.com/" + testClientID,
		})

		assert.Equal(t, StatusWarn, checkApplicationIDURI(app, testSiteURL).Status)
	})

	t.Run("without a site URL only the shape is verified", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkApplicationIDURI(healthyApp(t), "").Status)

		app := healthyApp(t)
		app.SetIdentifierUris([]string{"https://example.com/app"})
		assert.Equal(t, StatusWarn, checkApplicationIDURI(app, "").Status)
	})
}

func TestCheckExposedScope(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkExposedScope(healthyApp(t)).Status)
	})

	t.Run("scope missing", func(t *testing.T) {
		app := healthyApp(t)
		app.SetApi(models.NewApiApplication())

		result := checkExposedScope(app)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, ScopeName)
	})

	t.Run("no api object at all", func(t *testing.T) {
		app := healthyApp(t)
		app.SetApi(nil)

		assert.Equal(t, StatusFail, checkExposedScope(app).Status)
	})

	t.Run("scope disabled is fatal", func(t *testing.T) {
		app := healthyApp(t)
		app.GetApi().GetOauth2PermissionScopes()[0].SetIsEnabled(ptr(false))

		result := checkExposedScope(app)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "disabled")
	})

	t.Run("admin-only consent", func(t *testing.T) {
		app := healthyApp(t)
		app.GetApi().GetOauth2PermissionScopes()[0].SetTypeEscaped(ptr("Admin"))

		result := checkExposedScope(app)
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "consent type")
	})

	t.Run("missing consent copy", func(t *testing.T) {
		app := healthyApp(t)
		app.GetApi().GetOauth2PermissionScopes()[0].SetUserConsentDescription(ptr(""))

		assert.Equal(t, StatusWarn, checkExposedScope(app).Status)
	})
}

func TestCheckPreAuthorizedClients(t *testing.T) {
	t.Run("all clients authorized", func(t *testing.T) {
		result := checkPreAuthorizedClients(healthyApp(t))
		assert.Equal(t, StatusPass, result.Status)
	})

	t.Run("one client missing", func(t *testing.T) {
		app := healthyApp(t)
		preAuth := app.GetApi().GetPreAuthorizedApplications()
		app.GetApi().SetPreAuthorizedApplications(preAuth[1:])

		result := checkPreAuthorizedClients(app)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, strings.Join(result.Details, "\n"), getPreAuthorizedClients()[0])
	})

	t.Run("stale scope ID on a pre-authorized client", func(t *testing.T) {
		app := healthyApp(t)
		app.GetApi().GetPreAuthorizedApplications()[0].SetDelegatedPermissionIds([]string{uuid.NewString()})

		assert.Equal(t, StatusFail, checkPreAuthorizedClients(app).Status)
	})

	t.Run("extra client is reported but not fatal", func(t *testing.T) {
		app := healthyApp(t)
		extra := models.NewPreAuthorizedApplication()
		extra.SetAppId(ptr("00000000-0000-0000-0000-00000000dead"))
		extra.SetDelegatedPermissionIds([]string{testScopeID.String()})
		app.GetApi().SetPreAuthorizedApplications(append(app.GetApi().GetPreAuthorizedApplications(), extra))

		result := checkPreAuthorizedClients(app)
		assert.Equal(t, StatusPass, result.Status)
		assert.Contains(t, strings.Join(result.Details, "\n"), "00000000-0000-0000-0000-00000000dead")
	})

	t.Run("an uppercase client ID is neither missing nor unmanaged", func(t *testing.T) {
		app := healthyApp(t)
		app.GetApi().GetPreAuthorizedApplications()[0].SetAppId(ptr(strings.ToUpper(getPreAuthorizedClients()[0])))

		result := checkPreAuthorizedClients(app)
		assert.Equal(t, StatusPass, result.Status)
		assert.NotContains(t, strings.Join(result.Details, "\n"), "not managed by this tool")
	})

	t.Run("skipped when the scope does not exist", func(t *testing.T) {
		app := healthyApp(t)
		app.SetApi(models.NewApiApplication())

		assert.Equal(t, StatusSkip, checkPreAuthorizedClients(app).Status)
	})
}

func TestCheckRequiredPermissions(t *testing.T) {
	t.Run("all requested", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkRequiredPermissions(healthyApp(t), nil).Status)
	})

	t.Run("none requested", func(t *testing.T) {
		app := healthyApp(t)
		app.SetRequiredResourceAccess(nil)

		result := checkRequiredPermissions(app, nil)
		assert.Equal(t, StatusFail, result.Status)
		for _, perm := range getRequiredPermissions() {
			assert.Contains(t, result.Summary, perm.Name)
		}
	})

	t.Run("permission requested with the wrong type", func(t *testing.T) {
		// User.Read is a delegated permission; requesting it as an application
		// role does not grant what the plugin needs.
		app := healthyApp(t)
		access := app.GetRequiredResourceAccess()[0].GetResourceAccess()
		access[0].SetTypeEscaped(ptr(PermissionTypeRole))
		app.GetRequiredResourceAccess()[0].SetResourceAccess(access)

		result := checkRequiredPermissions(app, nil)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "User.Read")
	})
}

func TestCheckServicePrincipal(t *testing.T) {
	sp := models.NewServicePrincipal()
	sp.SetId(ptr("sp-object-id"))

	t.Run("enabled", func(t *testing.T) {
		sp.SetAccountEnabled(ptr(true))
		assert.Equal(t, StatusPass, checkServicePrincipal(sp, nil).Status)
	})

	t.Run("disabled", func(t *testing.T) {
		disabled := models.NewServicePrincipal()
		disabled.SetId(ptr("sp-object-id"))
		disabled.SetAccountEnabled(ptr(false))

		assert.Equal(t, StatusFail, checkServicePrincipal(disabled, nil).Status)
	})

	t.Run("missing", func(t *testing.T) {
		assert.Equal(t, StatusFail, checkServicePrincipal(nil, nil).Status)
	})

	t.Run("lookup failed", func(t *testing.T) {
		assert.Equal(t, StatusSkip, checkServicePrincipal(nil, assert.AnError).Status)
	})
}

func TestCheckDelegatedConsent(t *testing.T) {
	app := healthyApp(t)

	t.Run("granted tenant-wide", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkDelegatedConsent(healthyConsent(), app, "portal.azure.com").Status)
	})

	t.Run("granted for a single user only", func(t *testing.T) {
		consent := consentState{UserScopes: []string{"User.Read"}}

		result := checkDelegatedConsent(consent, app, "portal.azure.com")
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "individual users only")
		assert.Contains(t, result.Remediation, testClientID)
	})

	t.Run("a tenant-wide grant for another scope does not cover User.Read", func(t *testing.T) {
		consent := consentState{
			TenantWideScopes: []string{"openid", "profile"},
			UserScopes:       []string{"User.Read"},
		}

		result := checkDelegatedConsent(consent, app, "portal.azure.com")
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "User.Read")
	})

	t.Run("not granted is a warning, not a failure", func(t *testing.T) {
		// The plugin is app-only, so an unconsented delegated permission does
		// not stop it working and must not fail a CI health check.
		consent := consentState{TenantWideScopes: []string{"openid"}}

		result := checkDelegatedConsent(consent, app, "portal.azure.com")
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "User.Read")
		assert.Contains(t, result.Summary, "app-only")
	})

	t.Run("unreadable", func(t *testing.T) {
		result := checkDelegatedConsent(consentState{DelegatedErr: assert.AnError}, app, "portal.azure.com")
		assert.Equal(t, StatusSkip, result.Status)
	})

	t.Run("no service principal", func(t *testing.T) {
		result := checkDelegatedConsent(consentState{DelegatedErr: errNoServicePrincipal}, app, "portal.azure.com")
		assert.Equal(t, StatusSkip, result.Status)
		assert.Contains(t, result.Summary, "no service principal")
	})

	t.Run("a failure to read app roles does not hide the delegated result", func(t *testing.T) {
		consent := healthyConsent()
		consent.AppRoleErr = assert.AnError

		assert.Equal(t, StatusPass, checkDelegatedConsent(consent, app, "portal.azure.com").Status)
		assert.Equal(t, StatusSkip, checkAppRoleConsent(consent, app, "portal.azure.com").Status)
	})
}

func TestCheckAppRoleConsent(t *testing.T) {
	app := healthyApp(t)

	t.Run("granted", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkAppRoleConsent(healthyConsent(), app, "portal.azure.com").Status)
	})

	t.Run("partially granted", func(t *testing.T) {
		consent := healthyConsent()
		consent.AppRoleIDs = []string{PermissionTeamsActivitySendID}

		result := checkAppRoleConsent(consent, app, "portal.azure.com")
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "AppCatalog.Read.All")
		assert.NotContains(t, result.Summary, "TeamsActivity.Send")
	})

	t.Run("unreadable", func(t *testing.T) {
		assert.Equal(t, StatusSkip, checkAppRoleConsent(consentState{AppRoleErr: assert.AnError}, app, "portal.azure.com").Status)
	})
}

func TestCheckClientSecrets(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	t.Run("valid secret", func(t *testing.T) {
		app := healthyApp(t)
		app.SetPasswordCredentials([]models.PasswordCredentialable{
			passwordCredential("current", now.AddDate(0, 6, 0)),
		})

		assert.Equal(t, StatusPass, checkClientSecrets(app, now, 30).Status)
	})

	t.Run("no secrets at all", func(t *testing.T) {
		app := healthyApp(t)
		app.SetPasswordCredentials(nil)

		result := checkClientSecrets(app, now, 30)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Remediation, testClientID)
	})

	t.Run("every secret expired", func(t *testing.T) {
		app := healthyApp(t)
		app.SetPasswordCredentials([]models.PasswordCredentialable{
			passwordCredential("old", now.AddDate(0, -1, 0)),
		})

		assert.Equal(t, StatusFail, checkClientSecrets(app, now, 30).Status)
	})

	t.Run("a zero window suppresses the expiry warning", func(t *testing.T) {
		// The flag defaults to DefaultSecretWarningDays, so a zero reaching the
		// checks can only be an operator explicitly opting out.
		app := healthyApp(t)
		app.SetPasswordCredentials([]models.PasswordCredentialable{
			passwordCredential("expiring tomorrow", now.AddDate(0, 0, 1)),
		})

		in := healthyInputs(t)
		in.App = app
		in.Now = now
		in.SecretWarningDays = 0

		assert.Equal(t, StatusPass, statusOfCheck(t, runDoctorChecks(in), "Client secrets"))
	})

	t.Run("a negative window falls back to the default", func(t *testing.T) {
		app := healthyApp(t)
		app.SetPasswordCredentials([]models.PasswordCredentialable{
			passwordCredential("expiring tomorrow", now.AddDate(0, 0, 1)),
		})

		in := healthyInputs(t)
		in.App = app
		in.Now = now
		in.SecretWarningDays = -1

		assert.Equal(t, StatusWarn, statusOfCheck(t, runDoctorChecks(in), "Client secrets"))
	})

	t.Run("only secret expires soon", func(t *testing.T) {
		app := healthyApp(t)
		app.SetPasswordCredentials([]models.PasswordCredentialable{
			passwordCredential("expiring", now.AddDate(0, 0, 10)),
		})

		result := checkClientSecrets(app, now, 30)
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, strings.Join(result.Details, "\n"), "in 10 day(s)")
	})

	t.Run("expiring secret alongside a long-lived one", func(t *testing.T) {
		app := healthyApp(t)
		app.SetPasswordCredentials([]models.PasswordCredentialable{
			passwordCredential("expiring", now.AddDate(0, 0, 10)),
			passwordCredential("current", now.AddDate(1, 0, 0)),
			passwordCredential("old", now.AddDate(0, -3, 0)),
		})

		result := checkClientSecrets(app, now, 30)
		assert.Equal(t, StatusPass, result.Status)
		assert.Contains(t, strings.Join(result.Details, "\n"), "1 expired secret(s)")
	})
}

func TestCheckCertificates(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	t.Run("none configured", func(t *testing.T) {
		result := checkCertificates(healthyApp(t), now)
		assert.Equal(t, StatusPass, result.Status)
		assert.Contains(t, result.Summary, "None configured")
	})

	t.Run("expired certificate", func(t *testing.T) {
		app := healthyApp(t)
		credential := models.NewKeyCredential()
		credential.SetDisplayName(ptr("old-cert"))
		credential.SetEndDateTime(ptr(now.AddDate(0, -1, 0)))
		app.SetKeyCredentials([]models.KeyCredentialable{credential})

		assert.Equal(t, StatusWarn, checkCertificates(app, now).Status)
	})
}

func TestCheckOwners(t *testing.T) {
	assert.Equal(t, StatusPass, checkOwners([]string{"admin@example.com"}, nil).Status)
	assert.Equal(t, StatusWarn, checkOwners(nil, nil).Status)
	assert.Equal(t, StatusSkip, checkOwners(nil, assert.AnError).Status)
}

func TestCheckDuplicateApplications(t *testing.T) {
	app := healthyApp(t)

	assert.Equal(t, StatusPass, checkDuplicateApplications(app, nil, nil).Status)
	assert.Equal(t, StatusSkip, checkDuplicateApplications(app, nil, assert.AnError).Status)

	result := checkDuplicateApplications(app, []string{"other-client-id"}, nil)
	assert.Equal(t, StatusWarn, result.Status)
	assert.Contains(t, strings.Join(result.Details, "\n"), "other-client-id")
}

func TestDaysUntil(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, 0, daysUntil(now, now.Add(-time.Hour)))
	assert.Equal(t, 1, daysUntil(now, now.Add(time.Hour)))
	assert.Equal(t, 1, daysUntil(now, now.AddDate(0, 0, 1)))
	assert.Equal(t, 8, daysUntil(now, now.AddDate(0, 0, 7).Add(time.Hour)))
}

func TestDoctorReportFinalize(t *testing.T) {
	tests := []struct {
		name     string
		statuses []CheckStatus
		expected CheckStatus
	}{
		{name: "all pass", statuses: []CheckStatus{StatusPass, StatusPass}, expected: StatusPass},
		{name: "a skip degrades to warning", statuses: []CheckStatus{StatusPass, StatusSkip}, expected: StatusWarn},
		{name: "warning wins over pass", statuses: []CheckStatus{StatusPass, StatusWarn}, expected: StatusWarn},
		{name: "failure wins over warning", statuses: []CheckStatus{StatusWarn, StatusFail}, expected: StatusFail},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := &DoctorReport{}
			for i, status := range test.statuses {
				report.Add(CheckResult{Category: CategoryApplication, Name: string(rune('a' + i)), Status: status, Summary: "s"})
			}
			report.Finalize()

			assert.Equal(t, test.expected, report.Summary.Status)
			assert.Equal(t, len(test.statuses), report.Summary.Passed+report.Summary.Warned+report.Summary.Failed+report.Summary.Skipped)
		})
	}
}

func TestDoctorReportActionItemsAreFailuresFirst(t *testing.T) {
	report := &DoctorReport{}
	report.Add(CheckResult{Category: CategoryApplication, Name: "warn", Status: StatusWarn, Summary: "w"})
	report.Add(CheckResult{Category: CategoryApplication, Name: "pass", Status: StatusPass, Summary: "p"})
	report.Add(CheckResult{Category: CategoryConsent, Name: "fail", Status: StatusFail, Summary: "f"})

	items := report.actionItems()
	require.Len(t, items, 2)
	assert.Equal(t, "fail", items[0].Name)
	assert.Equal(t, "warn", items[1].Name)
}

// doctorReportFixture builds a small report that exercises every renderer branch.
func doctorReportFixture() *DoctorReport {
	report := &DoctorReport{
		GeneratedAt:         "2026-01-15 12:00:00 UTC",
		ToolVersion:         "test",
		Cloud:               "commercial",
		TenantID:            "tenant-123",
		ApplicationName:     "Mattermost for Teams",
		ApplicationClientID: testClientID,
	}

	report.Add(CheckResult{Category: CategoryApplication, Name: "Sign-in audience", Status: StatusPass, Summary: "Single tenant"})
	report.Add(CheckResult{
		Category:    CategoryConsent,
		Name:        "Admin consent (application permissions)",
		Status:      StatusFail,
		Summary:     "Not consented: AppCatalog.Read.All",
		Details:     []string{"TeamsActivity.Send: granted"},
		Remediation: "Grant admin consent in the Azure Portal",
	})
	report.Finalize()

	return report
}

func TestRenderDoctorReportHuman(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, RenderDoctorReport(&buf, doctorReportFixture(), "human"))

	output := buf.String()
	assert.Contains(t, output, "AZURE SETUP DOCTOR REPORT")
	assert.Contains(t, output, "Tenant ID:")
	assert.Contains(t, output, CategoryConsent)
	assert.Contains(t, output, "Not consented: AppCatalog.Read.All")
	assert.Contains(t, output, "TeamsActivity.Send: granted")
	assert.Contains(t, output, "ACTION ITEMS")
	assert.Contains(t, output, "1 passed · 0 warning(s) · 1 failed · 0 skipped")
}

func TestRenderDoctorReportJSON(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, RenderDoctorReport(&buf, doctorReportFixture(), "json"))

	var parsed DoctorReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))

	assert.Equal(t, StatusFail, parsed.Summary.Status)
	assert.Equal(t, 1, parsed.Summary.Failed)
	require.Len(t, parsed.Checks, 2)
	assert.Equal(t, "Sign-in audience", parsed.Checks[0].Name)
}

func TestRenderDoctorReportMarkdown(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, RenderDoctorReport(&buf, doctorReportFixture(), "markdown"))

	output := buf.String()
	assert.Contains(t, output, "# Azure Setup Doctor Report")
	assert.Contains(t, output, "| Status | Check | Result |")
	assert.Contains(t, output, "| FAIL |")
	assert.Contains(t, output, "## Action items")
}

func TestRenderDoctorReportUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	err := RenderDoctorReport(&buf, doctorReportFixture(), "yaml")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown report format")
}

func TestEscapeMarkdownCell(t *testing.T) {
	assert.Equal(t, `a \| b`, escapeMarkdownCell("a | b"))

	// A newline would terminate the table row, so whitespace is collapsed.
	assert.Equal(t, "line one line two", escapeMarkdownCell("line one\nline two"))
	assert.Equal(t, `odata error \| code: 403`, escapeMarkdownCell("odata error |\r\n\tcode: 403"))
}

func TestDescribePreAuthorizedClientCoversEveryConfiguredClient(t *testing.T) {
	for _, clientID := range getPreAuthorizedClients() {
		assert.NotEqualf(t, "unknown Microsoft client", describePreAuthorizedClient(clientID),
			"client ID %s has no human-readable description", clientID)
	}
}

func TestDescribeMissingApplication(t *testing.T) {
	assert.Contains(t, describeMissingApplication("Mattermost for Teams", ""), `"Mattermost for Teams"`)
	assert.Contains(t, describeMissingApplication("Mattermost for Teams", testClientID), testClientID)
}

func TestAdminConsentURL(t *testing.T) {
	assert.Equal(t,
		"https://portal.azure.us/#view/Microsoft_AAD_RegisteredApps/ApplicationMenuBlade/~/CallAnAPI/appId/"+testClientID,
		adminConsentURL("portal.azure.us", testClientID))

	assert.Contains(t, adminConsentURL("", testClientID), "portal.azure.com")
}

func TestCheckDirectoryRoles(t *testing.T) {
	t.Run("holds an admin role", func(t *testing.T) {
		directory := directoryContext{UserPrincipalName: "admin@example.com", AdminRoles: []string{"Global Administrator"}}
		assert.Equal(t, StatusPass, checkDirectoryRoles(directory, nil).Status)
	})

	t.Run("holds no admin role", func(t *testing.T) {
		assert.Equal(t, StatusWarn, checkDirectoryRoles(directoryContext{}, nil).Status)
	})

	t.Run("roles unreadable", func(t *testing.T) {
		assert.Equal(t, StatusSkip, checkDirectoryRoles(directoryContext{RolesErr: assert.AnError}, nil).Status)
	})

	t.Run("identity unreadable", func(t *testing.T) {
		result := checkDirectoryRoles(directoryContext{}, assert.AnError)
		assert.Equal(t, StatusSkip, result.Status)
		assert.Contains(t, result.Summary, "signed-in identity")
	})
}

func TestCheckApplicationLookup(t *testing.T) {
	report := &DoctorReport{
		ApplicationName:     "Mattermost for Teams",
		ApplicationClientID: testClientID,
		ApplicationObjectID: testObjectID,
	}

	assert.Equal(t, StatusPass, checkApplicationLookup(report, 1).Status)

	ambiguous := checkApplicationLookup(report, 3)
	assert.Equal(t, StatusFail, ambiguous.Status)
	assert.Contains(t, ambiguous.Summary, testObjectID)
	assert.Contains(t, ambiguous.Remediation, "--client-id")
}

func TestNormalizeReportFormat(t *testing.T) {
	tests := map[string]string{
		"":         "human",
		"human":    "human",
		"  JSON  ": "json",
		"md":       "markdown",
		"markdown": "markdown",
	}

	for input, expected := range tests {
		normalized, err := normalizeReportFormat(input)
		require.NoErrorf(t, err, "format %q should be accepted", input)
		assert.Equal(t, expected, normalized)
	}

	_, err := normalizeReportFormat("yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown report format")
}

func TestValidateSiteURL(t *testing.T) {
	tests := []struct {
		name    string
		siteURL string
		wantErr bool
	}{
		{name: "https URL", siteURL: "https://mattermost.example.com"},
		{name: "https URL with subpath", siteURL: "https://example.com/mattermost"},
		{name: "empty", siteURL: "", wantErr: true},
		{name: "no scheme", siteURL: "mattermost.example.com:8065/mm", wantErr: true},
		{name: "http", siteURL: "http://mattermost.example.com", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSiteURL(test.siteURL)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// doctorRequirementIDs are stand-in Graph role IDs for the doctor permissions,
// which the tool resolves from the Graph service principal at runtime.
var doctorRequirementIDs = map[string]string{
	"Application.Read.All": "3a1e4e11-0000-4000-8000-000000000001",
	"Directory.Read.All":   "3a1e4e11-0000-4000-8000-000000000002",
}

// graphNameMap builds the ID-to-name lookup the doctor derives from the Graph
// service principal.
func graphNameMap() map[string]string {
	names := make(map[string]string)
	for name, id := range doctorRequirementIDs {
		names[strings.ToLower(id)] = name
	}

	return names
}

// withDoctorRequirements adds the doctor permissions to an application the way
// `create --create-doctor-requirements` would.
func withDoctorRequirements(t *testing.T, app models.Applicationable) models.Applicationable {
	t.Helper()

	permissions := getRequiredPermissions()
	for name, id := range doctorRequirementIDs {
		permissions = append(permissions, requiredPermission{
			ResourceAppID: GraphResourceID,
			ResourceID:    id,
			Type:          PermissionTypeRole,
			Name:          name,
		})
	}

	access, err := buildRequiredResourceAccess(permissions)
	require.NoError(t, err)
	app.SetRequiredResourceAccess(access)

	return app
}

func TestCheckRequiredPermissionsIgnoresDoctorRequirements(t *testing.T) {
	app := withDoctorRequirements(t, healthyApp(t))

	t.Run("does not affect the status", func(t *testing.T) {
		result := checkRequiredPermissions(app, graphNameMap())
		assert.Equal(t, StatusPass, result.Status)
		assert.Contains(t, result.Summary, "All 3 required Graph permissions")
	})

	t.Run("names them instead of flagging them as unmanaged", func(t *testing.T) {
		details := strings.Join(checkRequiredPermissions(app, graphNameMap()).Details, "\n")

		assert.Contains(t, details, "Application.Read.All (Application): used by the doctor command")
		assert.Contains(t, details, "Directory.Read.All (Application): used by the doctor command")
		assert.NotContains(t, details, "not managed by this tool")
	})

	t.Run("falls back to the raw ID when the name is unknown", func(t *testing.T) {
		details := strings.Join(checkRequiredPermissions(app, nil).Details, "\n")

		assert.Contains(t, details, "not managed by this tool")
		assert.Contains(t, details, doctorRequirementIDs["Directory.Read.All"])
	})
}

func TestGraphPermissionNames(t *testing.T) {
	role := models.NewAppRole()
	roleID := uuid.MustParse("3a1e4e11-0000-4000-8000-000000000001")
	role.SetId(&roleID)
	role.SetValue(ptr("Application.Read.All"))

	scope := models.NewPermissionScope()
	scopeID := uuid.MustParse("3a1e4e11-0000-4000-8000-000000000009")
	scope.SetId(&scopeID)
	scope.SetValue(ptr("User.Read"))

	graphSP := models.NewServicePrincipal()
	graphSP.SetAppRoles([]models.AppRoleable{role})
	graphSP.SetOauth2PermissionScopes([]models.PermissionScopeable{scope})

	names := graphPermissionNames(graphSP)
	assert.Equal(t, "Application.Read.All", names[strings.ToLower(roleID.String())])
	assert.Equal(t, "User.Read", names[strings.ToLower(scopeID.String())])

	assert.Nil(t, graphPermissionNames(nil))
}

func TestDescribeGraphPermission(t *testing.T) {
	names := map[string]string{"abc-123": "Directory.Read.All"}

	assert.Equal(t, "Directory.Read.All", describeGraphPermission(names, "ABC-123"))
	assert.Equal(t, "unknown-id", describeGraphPermission(names, "unknown-id"))
	assert.Equal(t, "unknown-id", describeGraphPermission(nil, "unknown-id"))
}

func TestMergeExistingResourceAccess(t *testing.T) {
	desired, err := buildRequiredResourceAccess(getRequiredPermissions())
	require.NoError(t, err)

	t.Run("keeps a permission this run does not manage", func(t *testing.T) {
		existing := withDoctorRequirements(t, healthyApp(t))

		merged := mergeExistingResourceAccess(desired, existing)

		var ids []string
		for _, resource := range merged {
			for _, access := range resource.GetResourceAccess() {
				ids = append(ids, strings.ToLower(uuidString(access.GetId())))
			}
		}

		for name, id := range doctorRequirementIDs {
			assert.Containsf(t, ids, strings.ToLower(id), "%s should survive a re-run without the flag", name)
		}
		assert.Len(t, ids, len(getRequiredPermissions())+len(doctorRequirementIDs))
	})

	t.Run("does not duplicate a permission already desired", func(t *testing.T) {
		merged := mergeExistingResourceAccess(desired, healthyApp(t))

		total := 0
		for _, resource := range merged {
			total += len(resource.GetResourceAccess())
		}
		assert.Equal(t, len(getRequiredPermissions()), total)
	})

	t.Run("leaves the caller's desired list untouched", func(t *testing.T) {
		before := 0
		for _, resource := range desired {
			before += len(resource.GetResourceAccess())
		}

		mergeExistingResourceAccess(desired, withDoctorRequirements(t, healthyApp(t)))

		after := 0
		for _, resource := range desired {
			after += len(resource.GetResourceAccess())
		}
		assert.Equal(t, before, after, "merging must not grow the input")
	})

	t.Run("preserves a non-Graph resource as its own group", func(t *testing.T) {
		otherResourceID := "00000002-0000-0ff1-ce00-000000000000"
		access := models.NewResourceAccess()
		accessID := uuid.MustParse("3a1e4e11-0000-4000-8000-0000000000ff")
		access.SetId(&accessID)
		access.SetTypeEscaped(ptr(PermissionTypeRole))

		resource := models.NewRequiredResourceAccess()
		resource.SetResourceAppId(&otherResourceID)
		resource.SetResourceAccess([]models.ResourceAccessable{access})

		app := healthyApp(t)
		app.SetRequiredResourceAccess(append(app.GetRequiredResourceAccess(), resource))

		merged := mergeExistingResourceAccess(desired, app)
		require.Len(t, merged, 2)

		var resourceIDs []string
		for _, entry := range merged {
			resourceIDs = append(resourceIDs, derefString(entry.GetResourceAppId()))
		}
		assert.Contains(t, resourceIDs, otherResourceID)
	})

	t.Run("a differently cased type is the same permission", func(t *testing.T) {
		// Graph returns the casing whoever wrote the manifest used, so a
		// lower-case "role" must not be appended alongside our "Role" - the
		// PATCH would be rejected for a duplicate resourceAccess entry.
		app := healthyApp(t)
		for _, resource := range app.GetRequiredResourceAccess() {
			resource.SetResourceAppId(ptr(strings.ToUpper(GraphResourceID)))
			for _, access := range resource.GetResourceAccess() {
				access.SetTypeEscaped(ptr(strings.ToLower(derefString(access.GetTypeEscaped()))))
			}
		}

		merged := mergeExistingResourceAccess(desired, app)

		total := 0
		for _, resource := range merged {
			total += len(resource.GetResourceAccess())
		}
		assert.Equal(t, len(getRequiredPermissions()), total, "case alone must not create duplicate entries")
	})

	t.Run("an application with no permissions changes nothing", func(t *testing.T) {
		app := healthyApp(t)
		app.SetRequiredResourceAccess(nil)

		assert.Equal(t, desired, mergeExistingResourceAccess(desired, app))
		assert.Equal(t, desired, mergeExistingResourceAccess(desired, nil))
	})
}

func TestCheckApplicationIDURIIsCaseInsensitive(t *testing.T) {
	// Azure stores the URI with the casing it was created with, while the
	// expected value is derived from whatever --site-url the operator typed.
	app := healthyApp(t)
	app.SetIdentifierUris([]string{"api://mattermost.example.com/" + testClientID})

	result := checkApplicationIDURI(app, "https://Mattermost.Example.COM")
	assert.Equal(t, StatusPass, result.Status)
	assert.Equal(t, "api://mattermost.example.com/"+testClientID, result.Summary,
		"the summary should report the URI Azure actually holds")
}

func TestCheckApplicationIDURIPathIsCaseSensitive(t *testing.T) {
	// Entra issues the identifier URI verbatim as the Teams SSO audience and the
	// plugin compares it exactly, so a path that differs only in case is a
	// genuinely broken install and must not report PASS.
	app := healthyApp(t)
	app.SetIdentifierUris([]string{"api://corp.example.com/mattermost/" + testClientID})

	result := checkApplicationIDURI(app, "https://corp.example.com/Mattermost")
	assert.Equal(t, StatusFail, result.Status)
	assert.Contains(t, result.Summary, "api://corp.example.com/Mattermost/"+testClientID)
}

func TestEqualApplicationIDURI(t *testing.T) {
	tests := []struct {
		name  string
		a, b  string
		equal bool
	}{
		{name: "identical", a: "api://h.example.com/id", b: "api://h.example.com/id", equal: true},
		{name: "host case differs", a: "api://H.Example.COM/id", b: "api://h.example.com/id", equal: true},
		{name: "scheme case differs", a: "API://h.example.com/id", b: "api://h.example.com/id", equal: true},
		{name: "path case differs", a: "api://h.example.com/MM/id", b: "api://h.example.com/mm/id"},
		{name: "path present on one side only", a: "api://h.example.com/id", b: "api://h.example.com/mm/id"},
		{name: "different host", a: "api://a.example.com/id", b: "api://b.example.com/id"},
		{name: "non-api scheme falls back to exact match", a: "https://h.example.com", b: "https://H.example.com"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.equal, equalApplicationIDURI(test.a, test.b))
		})
	}
}

func TestCheckPreAuthorizedClientsFoldsScopeID(t *testing.T) {
	// A pre-authorized entry written by another tool can hold the scope ID in
	// uppercase; uuid.UUID.String() is always lowercase.
	app := healthyApp(t)
	app.GetApi().GetPreAuthorizedApplications()[0].SetDelegatedPermissionIds(
		[]string{strings.ToUpper(testScopeID.String())})

	result := checkPreAuthorizedClients(app)
	assert.Equal(t, StatusPass, result.Status, result.Summary)
}

func TestCheckRequiredPermissionsFoldsResourceAppID(t *testing.T) {
	app := healthyApp(t)
	for _, resource := range app.GetRequiredResourceAccess() {
		resource.SetResourceAppId(ptr(strings.ToUpper(GraphResourceID)))
	}

	result := checkRequiredPermissions(app, nil)
	assert.Equal(t, StatusPass, result.Status, result.Summary)
	assert.NotContains(t, strings.Join(result.Details, "\n"), "non-Graph permission")
}

func TestRenderDoctorReportFlagsAnIncompleteRun(t *testing.T) {
	report := &DoctorReport{GeneratedAt: "2026-01-15 12:00:00 UTC", Cloud: "commercial"}
	report.Add(CheckResult{Category: CategoryApplication, Name: "Sign-in audience", Status: StatusPass, Summary: "Single tenant"})
	report.Add(CheckResult{
		Category: CategoryConsent,
		Name:     "Admin consent (application permissions)",
		Status:   StatusSkip,
		Summary:  "Could not read the consent grants: insufficient privileges",
	})
	report.Finalize()

	assert.Equal(t, StatusWarn, report.Summary.Status, "an unverified check must not read as pass")

	var buf bytes.Buffer
	require.NoError(t, RenderDoctorReport(&buf, report, "human"))

	output := buf.String()
	assert.Contains(t, output, "1 check(s) could not run, so this report is incomplete.")
	assert.NotContains(t, output, "Everything checks out")
}

func TestIteratePagesToleratesANilCollection(t *testing.T) {
	// The generated SDK returns a literal nil for the interface return type on
	// an empty body ("if res == nil { return nil, nil }"), not a typed nil
	// pointer, so the guard in iteratePages sees a genuinely nil interface.
	// Callers rely on that: a 204 must read as "no results", not as a failed
	// check.
	var grants models.OAuth2PermissionGrantCollectionResponseable

	visited := 0
	err := iteratePages(context.Background(), nil, grants,
		models.CreateOAuth2PermissionGrantCollectionResponseFromDiscriminatorValue,
		func(models.OAuth2PermissionGrantable) { visited++ })

	require.NoError(t, err)
	assert.Zero(t, visited)
}

func TestReadConsentStateWithoutAServicePrincipal(t *testing.T) {
	state := readConsentState(context.Background(), nil, nil, nil, nil, nil)

	require.ErrorIs(t, state.DelegatedErr, errNoServicePrincipal)
	require.ErrorIs(t, state.AppRoleErr, errNoServicePrincipal)

	app := healthyApp(t)
	assert.Equal(t, StatusSkip, checkDelegatedConsent(state, app, "portal.azure.com").Status)
	assert.Equal(t, StatusSkip, checkAppRoleConsent(state, app, "portal.azure.com").Status)
}
