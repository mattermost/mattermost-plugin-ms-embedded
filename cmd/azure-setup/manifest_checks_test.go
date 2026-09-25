// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"
	"testing"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appForRealManifest builds a registration that agrees with the published
// manifest, so each test can break exactly one thing.
func appForRealManifest(t *testing.T) models.Applicationable {
	t.Helper()

	app := healthyApp(t)
	app.SetAppId(ptr(realManifestClientID))
	app.SetIdentifierUris([]string{realManifestResource})

	return app
}

// setResource replaces webApplicationInfo.resource in a raw manifest.
func setResource(raw map[string]any, resource string) {
	raw["webApplicationInfo"].(map[string]any)["resource"] = resource
}

func TestCheckManifestAudience(t *testing.T) {
	app := appForRealManifest(t)

	t.Run("matches", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkManifestAudience(loadRealManifest(t), app).Status)
	})

	t.Run("different host fails", func(t *testing.T) {
		raw := rawRealManifest(t)
		setResource(raw, "api://other.example.com/"+realManifestClientID)

		result := checkManifestAudience(writeManifestFile(t, raw), app)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "other.example.com")
	})

	t.Run("case-only difference warns rather than fails", func(t *testing.T) {
		// Microsoft documents no case-sensitivity rule for this comparison,
		// only a recommendation to use lowercase, so this must not be a hard
		// failure.
		raw := rawRealManifest(t)
		setResource(raw, "api://"+strings.ToUpper(realManifestHost)+"/"+realManifestClientID)

		result := checkManifestAudience(writeManifestFile(t, raw), app)
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "letter case only")
	})

	t.Run("missing resource fails", func(t *testing.T) {
		raw := rawRealManifest(t)
		setResource(raw, "")

		assert.Equal(t, StatusFail, checkManifestAudience(writeManifestFile(t, raw), app).Status)
	})

	t.Run("registration has no identifier URI", func(t *testing.T) {
		bare := appForRealManifest(t)
		bare.SetIdentifierUris(nil)

		assert.Equal(t, StatusFail, checkManifestAudience(loadRealManifest(t), bare).Status)
	})
}

func TestManifestSubpathDoubleSlashIsCaught(t *testing.T) {
	// The plugin joins host and path with "/" although url.Path already starts
	// with one, so a subpath install emits a doubled separator that does not
	// match the identifier URI azure-setup writes. This is the case the whole
	// feature exists to catch.
	const clientID = "11111111-2222-3333-4444-555555555555"

	app := healthyApp(t)
	app.SetAppId(ptr(clientID))
	app.SetIdentifierUris([]string{"api://example.com/mattermost/" + clientID})

	raw := rawRealManifest(t)
	setResource(raw, "api://example.com//mattermost/"+clientID)

	result := checkManifestAudience(writeManifestFile(t, raw), app)
	assert.Equal(t, StatusFail, result.Status)
	assert.Contains(t, result.Summary, "//mattermost")
}

func TestManifestPathDepthNote(t *testing.T) {
	t.Run("single segment is fine", func(t *testing.T) {
		assert.Nil(t, manifestPathDepthNote("api://example.com/"+realManifestClientID))
	})

	t.Run("subpath warns", func(t *testing.T) {
		note := manifestPathDepthNote("api://example.com/mattermost/" + realManifestClientID)
		require.NotNil(t, note)
		assert.Equal(t, StatusWarn, note.Status)
		assert.Contains(t, note.Summary, "multi-segment")
	})

	t.Run("non-api URI ignored", func(t *testing.T) {
		assert.Nil(t, manifestPathDepthNote("https://example.com/a/b"))
	})
}

func TestCheckManifestClientID(t *testing.T) {
	app := appForRealManifest(t)

	t.Run("matches", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkManifestClientID(loadRealManifest(t), app).Status)
	})

	t.Run("points at another registration", func(t *testing.T) {
		raw := rawRealManifest(t)
		raw["webApplicationInfo"].(map[string]any)["id"] = testClientID

		result := checkManifestClientID(writeManifestFile(t, raw), app)
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Remediation, testClientID)
	})

	t.Run("not a GUID", func(t *testing.T) {
		raw := rawRealManifest(t)
		raw["webApplicationInfo"].(map[string]any)["id"] = "not-a-guid"

		assert.Equal(t, StatusFail, checkManifestClientID(writeManifestFile(t, raw), app).Status)
	})
}

func TestCheckManifestAppID(t *testing.T) {
	t.Run("valid GUID", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkManifestAppID(loadRealManifest(t)).Status)
	})

	t.Run("not a GUID", func(t *testing.T) {
		raw := rawRealManifest(t)
		raw["id"] = "mattermost-app"

		result := checkManifestAppID(writeManifestFile(t, raw))
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "not a GUID")
	})

	t.Run("reusing the client ID is allowed", func(t *testing.T) {
		// Microsoft's own tab SSO sample uses one GUID for both, so this must
		// not be reported as a problem.
		raw := rawRealManifest(t)
		raw["id"] = realManifestClientID

		assert.Equal(t, StatusPass, checkManifestAppID(writeManifestFile(t, raw)).Status)
	})
}

func TestCheckManifestValidDomains(t *testing.T) {
	t.Run("contains the serving host", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkManifestValidDomains(loadRealManifest(t)).Status)
	})

	t.Run("host missing", func(t *testing.T) {
		raw := rawRealManifest(t)
		raw["validDomains"] = []any{"unrelated.example.com"}

		result := checkManifestValidDomains(writeManifestFile(t, raw))
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, realManifestHost)
	})

	t.Run("empty", func(t *testing.T) {
		raw := rawRealManifest(t)
		raw["validDomains"] = []any{}

		assert.Equal(t, StatusFail, checkManifestValidDomains(writeManifestFile(t, raw)).Status)
	})

	t.Run("URL instead of a bare domain warns", func(t *testing.T) {
		raw := rawRealManifest(t)
		raw["validDomains"] = []any{realManifestHost, "https://extra.example.com"}

		result := checkManifestValidDomains(writeManifestFile(t, raw))
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "bare domain")
	})

	t.Run("too many entries warns", func(t *testing.T) {
		domains := []any{realManifestHost}
		for i := 0; i <= MaxValidDomains; i++ {
			domains = append(domains, "d"+string(rune('a'+i))+".example.com")
		}

		raw := rawRealManifest(t)
		raw["validDomains"] = domains

		assert.Equal(t, StatusWarn, checkManifestValidDomains(writeManifestFile(t, raw)).Status)
	})
}

func TestCheckManifestContentURLs(t *testing.T) {
	t.Run("published manifest passes", func(t *testing.T) {
		result := checkManifestContentURLs(loadRealManifest(t))
		assert.Equal(t, StatusPass, result.Status)
		// The "about" tab has no contentUrl and must not be counted.
		assert.Contains(t, result.Summary, "2 tab URL(s)")
	})

	t.Run("host not in validDomains", func(t *testing.T) {
		raw := rawRealManifest(t)
		tabs := raw["staticTabs"].([]any)
		tabs[0].(map[string]any)["contentUrl"] = "https://elsewhere.example.com/plugins/" + PluginID + "/iframe/mattermostTab"

		result := checkManifestContentURLs(writeManifestFile(t, raw))
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "not in validDomains")
	})

	t.Run("wrong plugin path", func(t *testing.T) {
		raw := rawRealManifest(t)
		tabs := raw["staticTabs"].([]any)
		tabs[0].(map[string]any)["contentUrl"] = "https://" + realManifestHost + "/plugins/com.example.other/iframe/tab"

		result := checkManifestContentURLs(writeManifestFile(t, raw))
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, PluginID)
	})

	t.Run("http warns rather than fails", func(t *testing.T) {
		raw := rawRealManifest(t)
		tabs := raw["staticTabs"].([]any)
		for _, tab := range tabs {
			entry := tab.(map[string]any)
			if existing, ok := entry["contentUrl"].(string); ok {
				entry["contentUrl"] = strings.Replace(existing, "https://", "http://", 1)
			}
		}

		result := checkManifestContentURLs(writeManifestFile(t, raw))
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "not https")
	})

	for _, test := range []struct {
		name       string
		contentURL string
	}{
		{name: "relative URL", contentURL: "/plugins/" + PluginID + "/iframe/mattermostTab"},
		{name: "unsupported scheme", contentURL: "ftp://" + realManifestHost + "/plugins/" + PluginID + "/iframe/mattermostTab"},
	} {
		t.Run("rejects "+test.name, func(t *testing.T) {
			raw := rawRealManifest(t)
			raw["staticTabs"].([]any)[0].(map[string]any)["contentUrl"] = test.contentURL

			result := checkManifestContentURLs(writeManifestFile(t, raw))
			assert.Equal(t, StatusFail, result.Status)
			assert.Contains(t, result.Summary, "must use an absolute http or https URL")
		})
	}
}

func TestCheckManifestNotificationPermission(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkManifestNotificationPermission(loadRealManifest(t)).Status)
	})

	t.Run("absent warns", func(t *testing.T) {
		raw := rawRealManifest(t)
		delete(raw, "authorization")

		result := checkManifestNotificationPermission(writeManifestFile(t, raw))
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, ManifestActivityPermission)
	})

	t.Run("wrong type warns", func(t *testing.T) {
		raw := rawRealManifest(t)
		perms := raw["authorization"].(map[string]any)["permissions"].(map[string]any)["resourceSpecific"].([]any)
		perms[0].(map[string]any)["type"] = "Delegated"

		assert.Equal(t, StatusWarn, checkManifestNotificationPermission(writeManifestFile(t, raw)).Status)
	})
}

func TestCheckManifestVersion(t *testing.T) {
	t.Run("matches its schema", func(t *testing.T) {
		assert.Equal(t, StatusPass, checkManifestVersion(loadRealManifest(t)).Status)
	})

	t.Run("drifts from its schema", func(t *testing.T) {
		raw := rawRealManifest(t)
		raw["manifestVersion"] = "1.16"

		result := checkManifestVersion(writeManifestFile(t, raw))
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "1.22")
	})

	t.Run("no schema to compare against", func(t *testing.T) {
		raw := rawRealManifest(t)
		delete(raw, "$schema")

		assert.Equal(t, StatusPass, checkManifestVersion(writeManifestFile(t, raw)).Status)
	})
}

func TestCheckManifestPackage(t *testing.T) {
	load := func(t *testing.T, path string) *teamsManifest {
		t.Helper()
		manifest, err := loadManifest(path)
		require.NoError(t, err)

		return manifest
	}

	t.Run("complete package", func(t *testing.T) {
		manifest := load(t, realPackage(t, nil, defaultIcons()))
		assert.Equal(t, StatusPass, checkManifestPackage(manifest).Status)
	})

	t.Run("declared icon missing from the package", func(t *testing.T) {
		icons := defaultIcons()
		delete(icons, "icon-outline.png")

		result := checkManifestPackage(load(t, realPackage(t, nil, icons)))
		assert.Equal(t, StatusFail, result.Status)
		assert.Contains(t, result.Summary, "icon-outline.png")
	})

	t.Run("icon filenames follow the manifest, not a convention", func(t *testing.T) {
		mutate := func(raw map[string]any) {
			raw["icons"] = map[string]any{"color": "brand/color.png", "outline": "brand/outline.png"}
		}
		icons := map[string][]byte{
			"brand/color.png":   pngOfSize(IconColorSize, IconColorSize),
			"brand/outline.png": pngOfSize(IconOutlineSize, IconOutlineSize),
		}

		assert.Equal(t, StatusPass, checkManifestPackage(load(t, realPackage(t, mutate, icons))).Status)
	})

	t.Run("wrong dimensions warn", func(t *testing.T) {
		icons := defaultIcons()
		icons["icon-color.png"] = pngOfSize(64, 64)

		result := checkManifestPackage(load(t, realPackage(t, nil, icons)))
		assert.Equal(t, StatusWarn, result.Status)
		assert.Contains(t, result.Summary, "192x192")
	})
}

func TestManifestChecksAreAbsentWithoutAManifest(t *testing.T) {
	// The manifest is optional, so a run without one must not emit skips that
	// would downgrade the verdict.
	checks := runDoctorChecks(healthyInputs(t))

	for _, check := range checks {
		assert.NotEqual(t, CategoryManifest, check.Category)
	}
}

func TestManifestChecksReportAnUnreadableFile(t *testing.T) {
	in := healthyInputs(t)
	in.ManifestErr = assert.AnError

	checks := manifestChecks(in)
	require.Len(t, checks, 1)
	assert.Equal(t, StatusFail, checks[0].Status)
	assert.Equal(t, CategoryManifest, checks[0].Category)
}

func TestManifestChecksOnAConsistentSetup(t *testing.T) {
	in := healthyInputs(t)
	in.App = appForRealManifest(t)
	in.Manifest = loadRealManifest(t)

	for _, check := range manifestChecks(in) {
		assert.Equalf(t, StatusPass, check.Status, "check %q should pass: %s", check.Name, check.Summary)
	}
}

func TestDomainAllowedHandlesWildcards(t *testing.T) {
	// Teams permits wildcard entries, so a literal comparison would reject a
	// manifest that legitimately declares one.
	tests := []struct {
		name    string
		pattern string
		host    string
		allowed bool
	}{
		{name: "exact", pattern: "example.com", host: "example.com", allowed: true},
		{name: "exact folds case", pattern: "Example.COM", host: "example.com", allowed: true},
		{name: "wildcard segment", pattern: "*.example.com", host: "mm.example.com", allowed: true},
		{name: "wildcard matches one segment only", pattern: "*.example.com", host: "a.b.example.com"},
		{name: "wildcard does not match the bare domain", pattern: "*.example.com", host: "example.com"},
		{name: "different domain", pattern: "*.example.com", host: "mm.other.com"},
		{name: "partial segment is not a wildcard", pattern: "mm*.example.com", host: "mm1.example.com"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.allowed, domainAllowed([]string{test.pattern}, test.host))
		})
	}
}

func TestCheckManifestValidDomainsAcceptsAWildcard(t *testing.T) {
	raw := rawRealManifest(t)
	raw["validDomains"] = []any{"*.mattermost.com"}

	assert.Equal(t, StatusPass, checkManifestValidDomains(writeManifestFile(t, raw)).Status)
}

func TestManifestChecksRunWithoutARegistration(t *testing.T) {
	// When the application cannot be read, the manifest is still worth
	// reporting on: almost none of it depends on Azure.
	checks := manifestChecks(doctorInputs{Manifest: loadRealManifest(t)})

	var names []string
	for _, check := range checks {
		names = append(names, check.Name)
	}

	assert.Contains(t, names, "Teams app ID")
	assert.Contains(t, names, "Valid domains")
	assert.Contains(t, names, "Tab content URLs")
	assert.NotContains(t, names, "Published to the tenant", "the catalog lookup needs a live client")
}

func TestDomainMatchingIgnoresPorts(t *testing.T) {
	// The plugin builds validDomains from url.Host, which carries the port for
	// a server not on 443, while a tab URL host may or may not. Either side
	// having one must not break the comparison.
	assert.True(t, domainAllowed([]string{"mm.example.com:8065"}, "mm.example.com"))
	assert.True(t, domainAllowed([]string{"mm.example.com"}, "mm.example.com:8065"))
	assert.True(t, domainAllowed([]string{"*.example.com:8065"}, "mm.example.com"))
	assert.False(t, domainAllowed([]string{"other.example.com:8065"}, "mm.example.com"))
}

func TestCheckManifestContentURLsWithANonDefaultPort(t *testing.T) {
	raw := rawRealManifest(t)
	raw["validDomains"] = []any{"mm.example.com:8065"}
	raw["webApplicationInfo"].(map[string]any)["resource"] = "api://mm.example.com:8065/" + realManifestClientID

	tabs := raw["staticTabs"].([]any)
	for _, tab := range tabs {
		entry := tab.(map[string]any)
		if _, ok := entry["contentUrl"].(string); ok {
			entry["contentUrl"] = "https://mm.example.com:8065/plugins/" + PluginID + "/iframe/mattermostTab"
		}
	}

	manifest := writeManifestFile(t, raw)

	assert.Equal(t, StatusPass, checkManifestContentURLs(manifest).Status)

	// The port is still surfaced, because Teams expects a bare domain.
	domains := checkManifestValidDomains(manifest)
	assert.Equal(t, StatusWarn, domains.Status)
	assert.Contains(t, domains.Summary, "port")
}
