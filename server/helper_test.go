// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	goPlugin "github.com/hashicorp/go-plugin"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/msteams/clientmodels"
	"github.com/mattermost/mattermost-plugin-ms-embedded/server/msteams/mocks"
	"github.com/mattermost/mattermost-plugin-ms-embedded/server/store/pluginstore"
)

type testHelper struct {
	p                        *Plugin
	appClientMock            *mocks.Client
	clientMock               *mocks.Client
	websocketClients         map[string]*model.WebSocketClient
	websocketEventsWhitelist map[string][]model.WebsocketEventType
}

func setupTestHelper(t *testing.T) *testHelper {
	t.Helper()

	p := &Plugin{
		// These mocks are replaced later, but serve the plugin during early initialization
		msteamsAppClient:        &mocks.Client{},
		disableCheckCredentials: true,
	}
	p.pluginStore = pluginstore.NewPluginStore(p.API)

	th := &testHelper{
		p: p,
	}

	// Set up JWK to validate JWTs from Microsoft Teams.
	p.ensureJWKS()

	// ctx, and specifically cancel, gives us control over the plugin lifecycle
	ctx, cancel := context.WithCancel(context.Background())

	// reattachConfigCh is the means by which we get the Unix socket information to relay back
	// to the server and finish the reattachment.
	reattachConfigCh := make(chan *goPlugin.ReattachConfig)

	// closeCh tells us when the plugin exits and allows for cleanup.``
	closeCh := make(chan struct{})

	// plugin.ClientMain with options allows for reattachment.
	go plugin.ClientMain(
		th.p,
		plugin.WithTestContext(ctx),
		plugin.WithTestReattachConfigCh(reattachConfigCh),
		plugin.WithTestCloseCh(closeCh),
	)

	// Make sure the plugin shuts down normally with the test
	t.Cleanup(func() {
		cancel()

		select {
		case <-closeCh:
		case <-time.After(5 * time.Second):
			panic("plugin failed to close after 5 seconds")
		}
	})

	// Wait for the plugin to start and then reattach to the server.
	var reattachConfig *goPlugin.ReattachConfig
	select {
	case reattachConfig = <-reattachConfigCh:
	case <-time.After(5 * time.Second):
		t.Fatal("failed to get reattach config")
	}

	// Reattaching requires a local mode client.
	socketPath := os.Getenv("MM_LOCALSOCKETPATH")
	if socketPath == "" {
		socketPath = model.LocalModeSocketPath
	}
	clientLocal := model.NewAPIv4SocketClient(socketPath)

	// Set the plugin config before reattaching. This is unique to MS Teams because the plugin
	// currently fails to start without a valid configuration.
	_, _, err := clientLocal.PatchConfig(ctx, &model.Config{
		PluginSettings: model.PluginSettings{
			Plugins: map[string]map[string]any{
				manifest.Id: {
					"appVersion":                       "1.0.0",
					"appID":                            model.NewId(),
					"tenantID":                         model.NewId(),
					"appClientID":                      model.NewId(),
					"appClientSecret":                  model.NewId(),
					"appName":                          "test_app",
					"disableUserActivityNotifications": false,
				},
			},
		},
	})

	require.NoError(t, err)

	_, err = clientLocal.ReattachPlugin(ctx, &model.PluginReattachRequest{
		Manifest:             manifest,
		PluginReattachConfig: model.NewPluginReattachConfig(reattachConfig),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := clientLocal.DetachPlugin(ctx, manifest.Id)
		require.NoError(t, err)
	})

	th.Reset(t)
	return th
}

func (th *testHelper) clearDatabase(t *testing.T) {
	// No-op, there's no database in this plugin
}

func (th *testHelper) Reset(t *testing.T) *testHelper {
	t.Helper()

	// Wipe the tables for this plugin to ensure a clean slate. Note that we don't currently
	// touch any Mattermost tables.
	th.clearDatabase(t)

	appClientMock := &mocks.Client{}
	clientMock := &mocks.Client{}
	appClientMock.Test(t)
	clientMock.Test(t)

	th.appClientMock = appClientMock
	th.clientMock = clientMock

	appClientMock.On("GetApp", mock.AnythingOfType("string")).Return(&clientmodels.App{}, nil).Maybe()
	appClientMock.On("GetTenantID").Return("test-tenant-id").Maybe()
	appClientMock.On("GetTeamsAppIDByExternalID", mock.Anything, mock.Anything).Return("", errors.New("not found")).Maybe()
	clientMock.On("GetApp", mock.AnythingOfType("string")).Return(&clientmodels.App{}, nil).Maybe()
	clientMock.On("GetTenantID").Return("test-tenant-id").Maybe()

	th.p.msteamsAppClient = appClientMock

	t.Cleanup(func() {
		appClientMock.AssertExpectations(t)
		clientMock.AssertExpectations(t)

		// Ccheck the websocket event queue for unhandled events that might represent
		// unexpected behavior.
		unmatchedEvents := make(map[string][]*model.WebSocketEvent)

	nextWebsocketClient:
		for userID, websocketClient := range th.websocketClients {
			for {
				select {
				case event := <-websocketClient.EventChannel:
					if slices.Contains(th.websocketEventsWhitelist[userID], event.EventType()) {
						// Ignore whitelisted events.
						continue
					}
					unmatchedEvents[userID] = append(unmatchedEvents[userID], event)
				default:
					continue nextWebsocketClient
				}
			}
		}

		for userID, events := range unmatchedEvents {
			t.Logf("found %d unmatched websocket events for user %s", len(events), userID)
			for _, event := range events {
				t.Logf(" - %s", event.EventType())
			}
		}
		if len(unmatchedEvents) > 0 {
			t.Fail()
		}
	})

	return th
}

func (th *testHelper) SetupTeam(t *testing.T) *model.Team {
	t.Helper()

	teamName := model.NewRandomTeamName()
	team, appErr := th.p.API.CreateTeam(&model.Team{
		Name:        teamName,
		DisplayName: teamName,
		Type:        model.TeamOpen,
	})
	require.Nil(t, appErr)

	return team
}

func (th *testHelper) SetupUser(t *testing.T, team *model.Team) *model.User {
	t.Helper()

	username := model.NewUsername()

	user := &model.User{
		Email:         fmt.Sprintf("%s@example.com", username),
		Username:      username,
		Password:      "password",
		EmailVerified: true,
	}

	user, appErr := th.p.API.CreateUser(user)
	require.Nil(t, appErr)

	_, appErr = th.p.API.CreateTeamMember(team.Id, user.Id)
	require.Nil(t, appErr)

	return user
}

func (th *testHelper) SetupClient(t *testing.T, userID string) *model.Client4 {
	t.Helper()

	user, err := th.p.client.User.Get(userID)
	require.NoError(t, err)

	client := model.NewAPIv4Client(getSiteURL())

	// TODO: Don't hardcode "password"
	_, _, err = client.Login(context.TODO(), user.Username, "password")
	require.NoError(t, err)

	return client
}

func (th *testHelper) SetupDirectMessageChannel(t *testing.T, userID1, userID2 string) *model.Channel {
	t.Helper()

	require.NotEmpty(t, userID1)
	require.NotEmpty(t, userID2)

	channel, appErr := th.p.API.GetDirectChannel(userID1, userID2)
	require.Nil(t, appErr)
	return channel
}

func (th *testHelper) pluginURL(t *testing.T, paths ...string) string {
	baseURL, err := url.JoinPath(getSiteURL(), "plugins", pluginID)
	require.NoError(t, err)

	apiURL, err := url.JoinPath(baseURL, paths...)
	require.NoError(t, err)

	return apiURL
}
