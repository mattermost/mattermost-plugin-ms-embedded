// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

// Manifest expectations. TeamsActivity.Send.User is the resource-specific
// consent (RSC) permission the plugin declares so it can post to the activity
// feed; Microsoft documents RSC as the least-privileged alternative to the
// tenant-wide TeamsActivity.Send application permission, not as a companion to
// it, so its absence warns rather than fails.
const (
	ManifestActivityPermission = "TeamsActivity.Send.User"
	ManifestPermissionTypeApp  = "Application"

	// IconColorSize and IconOutlineSize are the dimensions Microsoft requires
	// for the two mandatory package icons.
	IconColorSize   = 192
	IconOutlineSize = 32

	// MaxValidDomains is the limit in the v1.22 schema the plugin's template
	// pins. Later schema versions raise it to 100.
	MaxValidDomains = 16
)

// checkManifestAudience compares the manifest's webApplicationInfo.resource
// against the registration's Application ID URI. Microsoft's guidance is to
// copy this value out of "Expose an API", so the two are the same string: if
// they diverge, the token Teams requests names an audience the registration
// does not answer for and single sign-on fails.
func checkManifestAudience(manifest *teamsManifest, app models.Applicationable) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "SSO audience (webApplicationInfo.resource)"}

	declared := manifest.WebApplicationInfo.Resource
	if declared == "" {
		result.Status = StatusFail
		result.Summary = "The manifest declares no webApplicationInfo.resource, so Teams cannot request an SSO token"
		result.Remediation = "Re-download the app package from the plugin settings page"

		return result
	}

	result.Details = append(result.Details, "manifest: "+declared)

	uris := app.GetIdentifierUris()
	if len(uris) == 0 {
		result.Status = StatusFail
		result.Summary = "The application has no Application ID URI to match " + declared
		result.Remediation = "Re-run `azure-setup create --site-url <mattermost-url> --client-id " + derefString(app.GetAppId()) + "`"

		return result
	}

	for _, uri := range uris {
		result.Details = append(result.Details, "registration: "+uri)
	}

	if slices.Contains(uris, declared) {
		result.Status = StatusPass
		result.Summary = "Matches the Application ID URI on the registration"

		return result
	}

	// A difference of case alone is called out separately. Microsoft documents
	// no case-sensitivity rule for this comparison - only "use lowercase
	// letters for the domain name" - so this is reported as a risk rather than
	// as a definite break.
	for _, uri := range uris {
		if strings.EqualFold(uri, declared) {
			result.Status = StatusWarn
			result.Summary = fmt.Sprintf("Differs from %s by letter case only", uri)
			result.Details = append(result.Details,
				"Microsoft documents no case-sensitivity rule here, but recommends lowercase throughout")
			result.Remediation = "Make both lowercase so the audience cannot depend on undocumented behaviour"

			return result
		}
	}

	result.Status = StatusFail
	result.Summary = fmt.Sprintf("Declares %s, which is not an Application ID URI on this registration", declared)
	result.Remediation = "Re-download the app package after confirming the plugin's site URL and client ID match the registration"

	return result
}

// manifestPathDepthNote flags an identifier URI with more than one path
// segment. Microsoft documents only api://<fqdn>/<app-id>, and Entra's own
// pattern guidance allows a single segment, so a Mattermost server behind a
// subpath produces a shape that is unsupported by omission rather than
// explicitly permitted.
func manifestPathDepthNote(resource string) *CheckResult {
	const scheme = "api://"

	if !strings.HasPrefix(strings.ToLower(resource), scheme) {
		return nil
	}

	_, rest, hasPath := strings.Cut(resource[len(scheme):], "/")
	if !hasPath || strings.Count(rest, "/") == 0 {
		return nil
	}

	return &CheckResult{
		Category: CategoryManifest,
		Name:     "Application ID URI path depth",
		Status:   StatusWarn,
		Summary:  fmt.Sprintf("%s has a multi-segment path", resource),
		Details: []string{
			"Microsoft documents only api://<domain>/<client-id> for Teams SSO",
			"a subpath Mattermost install produces this shape; it is unsupported by omission rather than prohibited",
		},
		Remediation: "Verify SSO works end to end on this deployment, or serve Mattermost from the domain root",
	}
}

// checkManifestClientID verifies the manifest points at this registration.
func checkManifestClientID(manifest *teamsManifest, app models.Applicationable) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "Client ID (webApplicationInfo.id)"}

	declared := manifest.WebApplicationInfo.ID
	actual := derefString(app.GetAppId())

	if declared == "" {
		result.Status = StatusFail
		result.Summary = "The manifest declares no webApplicationInfo.id"
		result.Remediation = "Re-download the app package from the plugin settings page"

		return result
	}

	if _, err := uuid.Parse(declared); err != nil {
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("%q is not a GUID; Teams requires one here", declared)
		result.Remediation = "Set the plugin's Client ID setting to the application's client ID and re-download the package"

		return result
	}

	if !strings.EqualFold(declared, actual) {
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("Points at %s, but this report describes %s", declared, actual)
		result.Remediation = "Re-download the app package, or re-run the doctor with --client-id " + declared + " to audit the application the manifest names"

		return result
	}

	result.Status = StatusPass
	result.Summary = "Matches the application under inspection"

	return result
}

// checkManifestAppID validates the Teams app identity.
//
// This is deliberately not compared against the client ID: Microsoft documents
// no relationship between the two, and its own tab SSO sample reuses one GUID
// for both, so requiring them to differ would flag a valid manifest.
func checkManifestAppID(manifest *teamsManifest) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "Teams app ID"}

	if manifest.ID == "" {
		result.Status = StatusFail
		result.Summary = "The manifest has no id"
		result.Remediation = "Set the plugin's App ID setting to a version 4 UUID and re-download the package"

		return result
	}

	if _, err := uuid.Parse(manifest.ID); err != nil {
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("%q is not a GUID, so Teams will reject the package", manifest.ID)
		result.Details = append(result.Details,
			"the plugin only checks this setting is non-empty, so an invalid value is accepted and surfaces here")
		result.Remediation = "Set the plugin's App ID setting to a version 4 UUID and re-download the package"

		return result
	}

	result.Status = StatusPass
	result.Summary = manifest.ID

	return result
}

// checkManifestValidDomains verifies the domains the tab is allowed to load
// content from. A host missing here renders the tab blank.
func checkManifestValidDomains(manifest *teamsManifest) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "Valid domains"}

	if len(manifest.ValidDomains) == 0 {
		result.Status = StatusFail
		result.Summary = "No validDomains are declared, so the tab will not load"
		result.Remediation = "Re-download the app package from the plugin settings page"

		return result
	}

	result.Details = append(result.Details, manifest.ValidDomains...)

	host := manifestResourceHost(manifest)
	if host != "" && !domainAllowed(manifest.ValidDomains, host) {
		result.Status = StatusFail
		result.Summary = fmt.Sprintf("%s serves the tab but is not in validDomains", host)
		result.Remediation = "Re-download the app package; the plugin derives validDomains from its site URL"

		return result
	}

	var problems []string

	// Store validation requires bare domains: "External domains declared for
	// your submission must not contain URLs."
	for _, domain := range manifest.ValidDomains {
		if strings.Contains(domain, "://") || strings.Contains(domain, "/") {
			problems = append(problems, fmt.Sprintf("%q is a URL, not a bare domain", domain))
			continue
		}

		if strings.Contains(domain, ":") {
			problems = append(problems, fmt.Sprintf("%q carries a port; Teams expects a bare domain", domain))
		}
	}

	if len(manifest.ValidDomains) > MaxValidDomains {
		problems = append(problems, fmt.Sprintf("%d entries exceeds the %d the pinned schema allows",
			len(manifest.ValidDomains), MaxValidDomains))
	}

	if len(problems) > 0 {
		result.Status = StatusWarn
		result.Summary = strings.Join(problems, "; ")
		result.Remediation = "Correct the site URL in the plugin settings and re-download the package"

		return result
	}

	result.Status = StatusPass
	result.Summary = fmt.Sprintf("%d domain(s), including the host that serves the tab", len(manifest.ValidDomains))

	return result
}

// checkManifestContentURLs verifies each tab points at this Mattermost server's
// plugin endpoint.
func checkManifestContentURLs(manifest *teamsManifest) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "Tab content URLs"}

	var checked int
	var failures, warnings []string

	for _, tab := range manifest.StaticTabs {
		// contentUrl is optional - the "about" tab legitimately has none.
		if tab.ContentURL == "" {
			continue
		}
		checked++

		parsed, err := url.Parse(tab.ContentURL)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: not a URL", tab.EntityID))
			continue
		}

		result.Details = append(result.Details, tab.EntityID+": "+tab.ContentURL)

		if !parsed.IsAbs() || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
			failures = append(failures, fmt.Sprintf("%s: must use an absolute http or https URL", tab.EntityID))
			continue
		}

		if !domainAllowed(manifest.ValidDomains, parsed.Hostname()) {
			failures = append(failures, fmt.Sprintf("%s: host %s is not in validDomains", tab.EntityID, parsed.Hostname()))
		}

		if !strings.Contains(parsed.Path, "/plugins/"+PluginID+"/") {
			failures = append(failures, fmt.Sprintf("%s: does not point at /plugins/%s/", tab.EntityID, PluginID))
		}

		// The schema regex makes the "s" optional and later versions allow
		// plain http, so this is a warning rather than a failure even though
		// Teams will not embed insecure content in practice.
		if !strings.EqualFold(parsed.Scheme, "https") {
			warnings = append(warnings, fmt.Sprintf("%s: %s is not https", tab.EntityID, parsed.Scheme))
		}
	}

	switch {
	case checked == 0:
		result.Status = StatusWarn
		result.Summary = "No static tab declares a content URL"
		result.Remediation = "Re-download the app package from the plugin settings page"
	case len(failures) > 0:
		result.Status = StatusFail
		result.Summary = strings.Join(failures, "; ")
		result.Remediation = "Re-download the app package after confirming the plugin's site URL"
	case len(warnings) > 0:
		result.Status = StatusWarn
		result.Summary = strings.Join(warnings, "; ")
		result.Remediation = "Serve Mattermost over HTTPS; Teams will not embed insecure content"
	default:
		result.Status = StatusPass
		result.Summary = fmt.Sprintf("%d tab URL(s) point at this server's plugin endpoint", checked)
	}

	return result
}

// checkManifestNotificationPermission verifies the resource-specific consent
// entry the plugin needs to post activity feed notifications.
func checkManifestNotificationPermission(manifest *teamsManifest) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "Notification permission"}

	permType, ok := manifest.resourceSpecificPermission(ManifestActivityPermission)
	if !ok {
		result.Status = StatusWarn
		result.Summary = ManifestActivityPermission + " is not declared, so activity feed notifications may not be delivered"
		result.Remediation = "Re-download the app package from the plugin settings page"

		return result
	}

	if !strings.EqualFold(permType, ManifestPermissionTypeApp) {
		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("%s is declared as %q; Microsoft supports it only as %q",
			ManifestActivityPermission, permType, ManifestPermissionTypeApp)
		result.Remediation = "Re-download the app package from the plugin settings page"

		return result
	}

	result.Status = StatusPass
	result.Summary = ManifestActivityPermission + " declared as " + ManifestPermissionTypeApp

	return result
}

// checkManifestVersion reports drift between the declared manifest version and
// the schema it pins. Any validator rejects a mismatch, because each schema
// constrains manifestVersion to a constant.
func checkManifestVersion(manifest *teamsManifest) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "Manifest version"}

	pinned := schemaVersion(manifest.Schema)

	switch {
	case manifest.ManifestVersion == "":
		result.Status = StatusWarn
		result.Summary = "No manifestVersion is declared"
	case pinned == "":
		result.Status = StatusPass
		result.Summary = "Version " + manifest.ManifestVersion + " (no $schema to compare against)"
	case pinned != manifest.ManifestVersion:
		result.Status = StatusWarn
		result.Summary = fmt.Sprintf("Declares version %s but pins the v%s schema, which constrains it to %s",
			manifest.ManifestVersion, pinned, pinned)
		result.Remediation = "Re-download the app package; a validator will reject this mismatch"
	default:
		result.Status = StatusPass
		result.Summary = "Version " + manifest.ManifestVersion + ", matching its $schema"
	}

	if manifest.Version != "" {
		result.Details = append(result.Details, "app version: "+manifest.Version)
	}

	return result
}

// checkManifestPackage verifies the app package carries the icons the manifest
// names. Teams rejects a package whose declared icons are absent, and the
// filenames are developer-chosen rather than fixed, so the paths come from the
// manifest rather than being hardcoded.
func checkManifestPackage(manifest *teamsManifest) CheckResult {
	result := CheckResult{Category: CategoryManifest, Name: "App package contents"}

	icons := []struct {
		field string
		name  string
		size  int
	}{
		{field: "icons.color", name: manifest.Icons.Color, size: IconColorSize},
		{field: "icons.outline", name: manifest.Icons.Outline, size: IconOutlineSize},
	}

	var failures, warnings []string

	for _, icon := range icons {
		if icon.name == "" {
			failures = append(failures, icon.field+" is not declared")
			continue
		}

		contents, ok := manifest.packageFile(icon.name)
		if !ok {
			failures = append(failures, fmt.Sprintf("%s names %s, which is not in the package", icon.field, icon.name))
			continue
		}

		width, height, valid := pngDimensions(contents)
		if !valid {
			warnings = append(warnings, icon.name+" is not a readable PNG")
			continue
		}

		result.Details = append(result.Details, fmt.Sprintf("%s: %s (%dx%d)", icon.field, icon.name, width, height))

		if width != icon.size || height != icon.size {
			warnings = append(warnings, fmt.Sprintf("%s is %dx%d; Microsoft requires %dx%d",
				icon.name, width, height, icon.size, icon.size))
		}
	}

	switch {
	case len(failures) > 0:
		result.Status = StatusFail
		result.Summary = strings.Join(failures, "; ")
		result.Remediation = "Re-download the app package from the plugin settings page"
	case len(warnings) > 0:
		result.Status = StatusWarn
		result.Summary = strings.Join(warnings, "; ")
		result.Remediation = "Replace the icons in the plugin settings with correctly sized PNGs"
	default:
		result.Status = StatusPass
		result.Summary = "Contains the manifest and both declared icons at the required sizes"
	}

	return result
}

// domainAllowed reports whether host is covered by the validDomains patterns.
//
// Teams permits wildcards, so a literal comparison would reject a manifest that
// legitimately declares "*.example.com". Per Microsoft's rules a wildcard must
// be the whole of its segment, and it matches exactly one segment.
func domainAllowed(patterns []string, host string) bool {
	for _, pattern := range patterns {
		if domainPatternMatches(pattern, host) {
			return true
		}
	}

	return false
}

func domainPatternMatches(pattern, host string) bool {
	// The plugin derives validDomains from url.Host, which carries the port for
	// a server not on 443, while a tab URL host may or may not. Comparing
	// without the port makes the two agree either way.
	pattern = stripPort(pattern)
	host = stripPort(host)

	if strings.EqualFold(pattern, host) {
		return true
	}

	patternSegments := strings.Split(pattern, ".")
	hostSegments := strings.Split(host, ".")

	if len(patternSegments) != len(hostSegments) {
		return false
	}

	for i, segment := range patternSegments {
		if segment == "*" {
			continue
		}

		if !strings.EqualFold(segment, hostSegments[i]) {
			return false
		}
	}

	return true
}

// stripPort removes a trailing :port from a host or domain pattern.
func stripPort(host string) string {
	if trimmed, _, found := strings.Cut(host, ":"); found {
		return trimmed
	}

	return host
}

// manifestResourceHost returns the host the tab is served from, taken from the
// identifier URI so it reflects what the manifest itself claims.
func manifestResourceHost(manifest *teamsManifest) string {
	const scheme = "api://"

	resource := manifest.WebApplicationInfo.Resource
	if !strings.HasPrefix(strings.ToLower(resource), scheme) {
		return ""
	}

	host, _, _ := strings.Cut(resource[len(scheme):], "/")

	return host
}
