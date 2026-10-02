// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ms-embedded/assets"
)

// TeamsJSVersion and TeamsJSIntegrity are independent constants; a version bump that
// leaves the hash stale fails SRI validation and blocks the SDK on every page at once.
func TestTeamsJSIntegrityMatchesCDN(t *testing.T) {
	if testing.Short() {
		t.Skip("requires network access to the Office CDN")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://res.cdn.office.net/teams-js/%s/js/MicrosoftTeams.min.js", TeamsJSVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	sum := sha512.Sum384(body)
	require.Equal(t, TeamsJSIntegrity, "sha384-"+base64.StdEncoding.EncodeToString(sum[:]),
		"TeamsJSIntegrity is stale for teams-js %s", TeamsJSVersion)
}

func TestTemplatesUseTeamsJSConstants(t *testing.T) {
	templates, err := fs.Glob(assets.Templates, "*.html.tmpl")
	require.NoError(t, err)
	require.NotEmpty(t, templates)

	scriptTag := regexp.MustCompile(`<script[^>]*teams-js[^>]*>`)

	tags := 0
	for _, path := range templates {
		content, err := fs.ReadFile(assets.Templates, path)
		require.NoError(t, err)

		for _, tag := range scriptTag.FindAllString(string(content), -1) {
			tags++
			assert.Contains(t, tag, "teams-js/{{.TeamsJSVersion}}/", "%s hardcodes the teams-js version", path)
			assert.Contains(t, tag, "{{.TeamsJSIntegrityAttr}}", "%s hardcodes the teams-js integrity hash", path)
		}
	}

	require.NotZero(t, tags, "found no teams-js script tags to check")
}

func TestNotificationNavigateContract(t *testing.T) {
	preview, err := fs.ReadFile(assets.Templates, "iframe_notification_preview.html.tmpl")
	require.NoError(t, err)
	previewStr := string(preview)

	postToShell := regexp.MustCompile(`window\.parent\.postMessage\(\{\s*type: 'mattermost_notification_navigate',\s*subPageId: 'post_\{\{\.Post\.Id\}\}'\s*\},\s*"\{\{\.SiteURL\}\}"\)`)
	assert.Regexp(t, postToShell, previewStr, "preview must post the navigate message to the SiteURL origin only")
	assert.NotContains(t, previewStr, "teams-js/")
	assert.NotContains(t, previewStr, "app.initialize")

	handler := shellMessageHandler(t, "mattermost_notification_navigate")
	assertChecksPrecede(t, handler, "navigateToApp",
		"isValidOrigin(event.origin, domainRoot)", "event.source !== iframe.contentWindow", "subPageId !== 'string'")

	assert.Contains(t, handler, "pageId: 'Mattermost'")
	assert.Contains(t, handler, "appId: '{{.TeamsAppID}}'")
	assert.NotContains(t, handler, "event.data.appId")
}

func TestShellAuthMessageChecks(t *testing.T) {
	tests := []struct {
		msgType string
		actions []string
	}{
		{"mattermost_external_auth_login", []string{"event.source.postMessage", "iframe.src ="}},
		{"mattermost_external_auth_complete", []string{"iframe.src ="}},
	}

	for _, tc := range tests {
		t.Run(tc.msgType, func(t *testing.T) {
			handler := shellMessageHandler(t, tc.msgType)
			for _, action := range tc.actions {
				assertChecksPrecede(t, handler, action,
					"isValidOrigin(event.origin, domainRoot)", "event.source !== iframe.contentWindow")
			}
		})
	}
}

func shellMessageHandler(t *testing.T, msgType string) string {
	t.Helper()

	shell, err := fs.ReadFile(assets.Templates, "iframe.html.tmpl")
	require.NoError(t, err)
	shellStr := string(shell)

	start := strings.Index(shellStr, "if (event.data.type === '"+msgType+"') {")
	require.GreaterOrEqual(t, start, 0, "shell must handle %s", msgType)
	// The branch's closing brace is the first one at its own indentation.
	end := strings.Index(shellStr[start:], "\n        }\n")
	require.Positive(t, end, "could not find the end of the %s handler", msgType)

	return shellStr[start : start+end]
}

func assertChecksPrecede(t *testing.T, handler, action string, checks ...string) {
	t.Helper()

	actionIdx := strings.Index(handler, action)
	require.GreaterOrEqual(t, actionIdx, 0, "handler is missing %q", action)
	for _, check := range checks {
		checkIdx := strings.Index(handler, check)
		require.GreaterOrEqual(t, checkIdx, 0, "handler is missing %q", check)
		assert.Less(t, checkIdx, actionIdx, "%q must run before %q", check, action)
	}
}
