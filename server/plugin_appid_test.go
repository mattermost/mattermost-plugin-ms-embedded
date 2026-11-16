// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/msteams/mocks"
	"github.com/mattermost/mattermost-plugin-ms-embedded/server/store/pluginstore"
)

func TestGetTeamsAppIDThrottlesFailedLookups(t *testing.T) {
	const manifestID = "manifest-id"

	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(nil, nil)
	api.On("KVSet", mock.Anything, []byte("catalog-id")).Return(nil)
	api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	client := &mocks.Client{}
	client.On("GetTenantID").Return("tenant-id")
	client.On("GetTeamsAppIDByExternalID", mock.Anything, manifestID).Return("", errors.New("forbidden")).Once()

	p := &Plugin{}
	p.API = api
	p.pluginStore = pluginstore.NewPluginStore(api)
	p.msteamsAppClient = client
	p.setConfiguration(&configuration{AppID: manifestID})

	_, err := p.getTeamsAppID()
	require.Error(t, err)
	assert.NotErrorIs(t, err, errAppIDLookupThrottled)

	_, err = p.getTeamsAppID()
	assert.ErrorIs(t, err, errAppIDLookupThrottled)
	client.AssertNumberOfCalls(t, "GetTeamsAppIDByExternalID", 1)

	p.appIDLookupFailedAt = time.Now().Add(-appIDLookupRetryInterval)
	client.On("GetTeamsAppIDByExternalID", mock.Anything, manifestID).Return("catalog-id", nil).Once()

	appID, err := p.getTeamsAppID()
	require.NoError(t, err)
	assert.Equal(t, "catalog-id", appID)
	client.AssertNumberOfCalls(t, "GetTeamsAppIDByExternalID", 2)
	api.AssertCalled(t, "KVSet", mock.Anything, []byte("catalog-id"))
}
