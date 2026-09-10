// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package assets

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamsJSCDNConsistentAcrossTemplates(t *testing.T) {
	templates := []struct {
		name    string
		content string
	}{
		{name: "iframe.html.tmpl", content: IFrameHTMLTemplate},
		{name: "iframe_notification_preview.html.tmpl", content: IFrameNotificationPreviewHTMLTemplate},
		{name: "sso_complete.html.tmpl", content: SSOCompleteHTMLTemplate},
	}

	// Capture version and integrity from the Teams JS script tag, regardless of attribute order.
	scriptRe := regexp.MustCompile(`(?s)<script\b([^>]*)>`)
	srcRe := regexp.MustCompile(`src="https://res\.cdn\.office\.net/teams-js/([^/]+)/js/MicrosoftTeams\.min\.js"`)
	integrityRe := regexp.MustCompile(`integrity="(sha384-[^"]+)"`)

	var version, integrity string
	for i, tmpl := range templates {
		var foundVersion, foundIntegrity string
		for _, scriptMatch := range scriptRe.FindAllStringSubmatch(tmpl.content, -1) {
			attrs := scriptMatch[1]
			srcMatch := srcRe.FindStringSubmatch(attrs)
			if srcMatch == nil {
				continue
			}
			integMatch := integrityRe.FindStringSubmatch(attrs)
			require.Lenf(t, integMatch, 2, "SRI integrity missing for teams-js in %s", tmpl.name)
			foundVersion = srcMatch[1]
			foundIntegrity = integMatch[1]
			break
		}
		require.NotEmptyf(t, foundVersion, "teams-js URL missing in %s", tmpl.name)

		if i == 0 {
			version = foundVersion
			integrity = foundIntegrity
			continue
		}

		assert.Equalf(t, version, foundVersion, "teams-js version drift in %s", tmpl.name)
		assert.Equalf(t, integrity, foundIntegrity, "SRI drift in %s", tmpl.name)
	}

	assert.NotEmpty(t, version)
	assert.NotEmpty(t, integrity)
}
