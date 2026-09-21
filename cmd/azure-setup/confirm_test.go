// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runPreflight answers the confirmation prompt with the given input and returns
// what the operator was shown.
func runPreflight(t *testing.T, permissions []requiredPermission, answer string) (string, error) {
	t.Helper()

	stdinR, stdinW, err := os.Pipe()
	require.NoError(t, err)

	_, err = stdinW.WriteString(answer)
	require.NoError(t, err)
	require.NoError(t, stdinW.Close())

	origStdin := os.Stdin
	os.Stdin = stdinR
	defer func() { os.Stdin = origStdin }()

	config := &SetupConfig{
		MattermostSiteURL: testSiteURL,
		AppName:           "Mattermost for Teams",
		SecretExpiration:  12,
	}

	var confirmErr error
	_, stderr := captureStreams(t, func() {
		confirmErr = showPreflightConfirmation(config, nil, permissions)
	})

	return stderr, confirmErr
}

func TestShowPreflightConfirmationListsEveryPermissionRequested(t *testing.T) {
	// The operator must approve the same set that gets requested. The doctor
	// permissions are the two that widen what this application's credentials
	// can read, so the prompt omitting them would be the worst case.
	permissions := append(getRequiredPermissions(),
		requiredPermission{
			ResourceAppID: GraphResourceID,
			ResourceID:    "3a1e4e11-0000-4000-8000-000000000001",
			Type:          PermissionTypeRole,
			Name:          "Application.Read.All",
		},
		requiredPermission{
			ResourceAppID: GraphResourceID,
			ResourceID:    "3a1e4e11-0000-4000-8000-000000000002",
			Type:          PermissionTypeRole,
			Name:          "Directory.Read.All",
		},
	)

	output, err := runPreflight(t, permissions, "y\n")
	require.NoError(t, err)

	for _, perm := range permissions {
		assert.Containsf(t, output, perm.Name, "the prompt must list %s before asking for approval", perm.Name)
	}

	assert.Contains(t, output, "User.Read (Delegated)")
	assert.Contains(t, output, "TeamsActivity.Send (Application)")
	assert.Contains(t, output, "tenant-wide directory read",
		"the doctor permissions need the note explaining what approving them costs")
}

func TestShowPreflightConfirmationWithoutDoctorPermissions(t *testing.T) {
	output, err := runPreflight(t, getRequiredPermissions(), "y\n")
	require.NoError(t, err)

	assert.NotContains(t, output, "Application.Read.All")
	assert.NotContains(t, output, "tenant-wide directory read")
}

func TestShowPreflightConfirmationCancels(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", "no\n"} {
		t.Run(strings.TrimSpace(answer)+"|", func(t *testing.T) {
			_, err := runPreflight(t, getRequiredPermissions(), answer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cancelled")
		})
	}
}
