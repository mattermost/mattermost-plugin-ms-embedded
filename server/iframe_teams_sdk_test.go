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
