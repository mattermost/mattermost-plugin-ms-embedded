// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package msteams

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/microsoft/kiota-abstractions-go/authentication"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/msteams/clientmodels"
)

func TestConvertToMessage(t *testing.T) {
	teamsUserID := model.NewId()
	teamsUserDisplayName := "mockTeamsUserDisplayName"
	teamsReplyID := "mockTeamsReplyID"
	content := "mockContent"
	subject := "mockSubject"
	reactionType := "mockReactionType"
	attachmentContent := "mockAttachmentContent"
	attachmentContentType := "mockAttachmentContentType"
	attachmentName := "mockAttachmentName"
	attachmentURL := "mockAttachmentURL"
	mentionID := int32(0)
	channelID := model.NewId()
	for _, test := range []struct {
		Name           string
		ChatMessage    models.ChatMessageable
		ExpectedResult clientmodels.Message
	}{
		{
			Name: "ConvertToMessage: With data filled",
			ChatMessage: func() models.ChatMessageable {
				from := models.NewIdentitySet()
				user := models.NewIdentity()
				user.SetId(&teamsUserID)
				user.SetDisplayName(&teamsUserDisplayName)
				from.SetUser(user)

				body := models.NewItemBody()
				body.SetContent(&content)

				attachment := models.NewChatMessageAttachment()
				attachment.SetContentType(&attachmentContentType)
				attachment.SetContent(&attachmentContent)
				attachment.SetName(&attachmentName)
				attachment.SetContentUrl(&attachmentURL)

				reactionUserSet := models.NewIdentitySet()
				reactionUser := models.NewIdentity()
				reactionUser.SetId(&teamsUserID)
				reactionUserSet.SetUser(reactionUser)
				reaction := models.NewChatMessageReaction()
				reaction.SetUser(reactionUserSet)
				reaction.SetReactionType(&reactionType)

				mention := models.NewChatMessageMention()
				mentionedID := mentionID
				mention.SetId(&mentionedID)

				identity := models.NewIdentity()
				identity.SetId(&teamsUserID)
				identity.SetDisplayName(&teamsUserDisplayName)

				additionalData := map[string]any{
					"userIdentityType": "aadUser",
				}

				identity.SetAdditionalData(additionalData)
				mentioned := models.NewChatMessageMentionedIdentitySet()
				mentioned.SetUser(identity)

				mention.SetMentionText(&teamsUserDisplayName)
				mention.SetMentioned(mentioned)

				message := models.NewChatMessage()
				message.SetFrom(from)
				message.SetReplyToId(&teamsReplyID)
				message.SetBody(body)
				message.SetSubject(&subject)
				message.SetCreatedDateTime(&time.Time{})
				message.SetLastModifiedDateTime(&time.Time{})
				message.SetAttachments([]models.ChatMessageAttachmentable{attachment})
				message.SetReactions([]models.ChatMessageReactionable{reaction})
				message.SetMentions([]models.ChatMessageMentionable{mention})
				return message
			}(),
			ExpectedResult: clientmodels.Message{
				UserID:          teamsUserID,
				UserDisplayName: teamsUserDisplayName,
				ReplyToID:       teamsReplyID,
				Text:            content,
				Subject:         subject,
				CreateAt:        time.Time{},
				LastUpdateAt:    time.Time{},
				Attachments: []clientmodels.Attachment{
					{
						ContentType: attachmentContentType,
						Content:     attachmentContent,
						Name:        attachmentName,
						ContentURL:  attachmentURL,
					},
				},
				Mentions: []clientmodels.Mention{
					{
						ID:            mentionID,
						UserID:        teamsUserID,
						MentionedText: teamsUserDisplayName,
					},
				},
				Reactions: []clientmodels.Reaction{
					{
						Reaction: reactionType,
						UserID:   teamsUserID,
					},
				},
				ChannelID: channelID,
				TeamID:    "mockTeamsTeamID",
				ChatID:    "mockChatID",
			},
		},
		{
			Name: "ConvertToMessage: With no data filled",
			ChatMessage: func() models.ChatMessageable {
				message := models.NewChatMessage()
				message.SetCreatedDateTime(&time.Time{})
				message.SetLastModifiedDateTime(&time.Time{})
				return message
			}(),
			ExpectedResult: clientmodels.Message{
				Attachments:  []clientmodels.Attachment{},
				Reactions:    []clientmodels.Reaction{},
				Mentions:     []clientmodels.Mention{},
				CreateAt:     time.Time{},
				LastUpdateAt: time.Time{},
				ChannelID:    channelID,
				TeamID:       "mockTeamsTeamID",
				ChatID:       "mockChatID",
			},
		},
	} {
		t.Run(test.Name, func(t *testing.T) {
			assert := assert.New(t)
			resp := convertToMessage(test.ChatMessage, "mockTeamsTeamID", channelID, "mockChatID")

			assert.Equal(test.ExpectedResult, *resp)
		})
	}
}

func TestGetResourceIds(t *testing.T) {
	for _, test := range []struct {
		Name           string
		Resource       string
		ExpectedResult clientmodels.ActivityIDs
	}{
		{
			Name:     "GetResourceIds: With prefix chats(",
			Resource: "chats('19:8ea0e38b-efb3-4757-924a-5f94061cf8c2_97f62344-57dc-409c-88ad-c4af14158ff5@unq.gbl.spaces')/messages('1612289765949')",
			ExpectedResult: clientmodels.ActivityIDs{
				ChatID:    "19:8ea0e38b-efb3-4757-924a-5f94061cf8c2_97f62344-57dc-409c-88ad-c4af14158ff5@unq.gbl.spaces",
				MessageID: "1612289765949",
			},
		},
		{
			Name:     "GetResourceIds: Without prefix chats(",
			Resource: "teams('fbe2bf47-16c8-47cf-b4a5-4b9b187c508b')/channels('19:4a95f7d8db4c4e7fae857bcebe0623e6@thread.tacv2')/messages('1612293113399')/replies('19:zOtXfpDMWANo7-9CHuzHdM7WPSamQejH0Vydj0U-Yho1')",
			ExpectedResult: clientmodels.ActivityIDs{
				TeamID:    "fbe2bf47-16c8-47cf-b4a5-4b9b187c508b",
				ChannelID: "19:4a95f7d8db4c4e7fae857bcebe0623e6@thread.tacv2",
				MessageID: "1612293113399",
				ReplyID:   "19:zOtXfpDMWANo7-9CHuzHdM7WPSamQejH0Vydj0U-Yho1",
			},
		},
		{
			Name:           "GetResourceIds: Resource with multiple '/'",
			Resource:       "/////19:4a95f7d8db4c4e7fae857bcebe0623e6@thread.tacv2///",
			ExpectedResult: clientmodels.ActivityIDs{},
		},
		{
			Name:           "GetResourceIds: Empty resource",
			ExpectedResult: clientmodels.ActivityIDs{},
		},
		{
			Name:           "GetResourceIds: Resource with small length",
			Resource:       "ID",
			ExpectedResult: clientmodels.ActivityIDs{},
		},
		{
			Name:           "GetResourceIds: Resource with large length",
			Resource:       "very-long-teams-ID-with-very-long-chat-ID",
			ExpectedResult: clientmodels.ActivityIDs{},
		},
	} {
		t.Run(test.Name, func(t *testing.T) {
			assert := assert.New(t)
			resp := GetResourceIDs(test.Resource)
			assert.Equal(test.ExpectedResult, resp)
		})
	}
}

func TestCheckGroupChat(t *testing.T) {
	makeMember := func(userID, email any) models.ConversationMemberable {
		m := models.NewConversationMember()
		_ = m.GetBackingStore().Set("userId", userID)
		_ = m.GetBackingStore().Set("email", email)
		return m
	}
	makeChat := func(id *string, members ...models.ConversationMemberable) models.Chatable {
		c := models.NewChat()
		c.SetId(id)
		c.SetMembers(members)
		return c
	}

	chatID := "chat-1"
	u1, e1 := "u1", "a@example.com"
	u2, e2 := "u2", "b@example.com"

	t.Run("nil chat ID returns nil", func(t *testing.T) {
		c := makeChat(nil, makeMember(&u1, &e1))
		assert.Nil(t, checkGroupChat(c, []string{"u1"}))
	})

	t.Run("member count mismatch returns nil", func(t *testing.T) {
		c := makeChat(&chatID, makeMember(&u1, &e1))
		assert.Nil(t, checkGroupChat(c, []string{"u1", "u2"}))
	})

	t.Run("all members match returns chat", func(t *testing.T) {
		c := makeChat(&chatID, makeMember(&u1, &e1), makeMember(&u2, &e2))
		got := checkGroupChat(c, []string{"u1", "u2"})
		assert.NotNil(t, got)
		assert.Equal(t, chatID, got.ID)
		assert.Equal(t, "G", got.Type)
		assert.Len(t, got.Members, 2)
	})

	t.Run("non-*string userId does not panic", func(t *testing.T) {
		// userId stored as a plain string instead of *string would panic the
		// unguarded type assertion before the fix.
		c := makeChat(&chatID, makeMember("u1", &e1), makeMember(&u2, &e2))
		var got *clientmodels.Chat
		assert.NotPanics(t, func() { got = checkGroupChat(c, []string{"u1", "u2"}) })
		assert.Nil(t, got)
	})

	t.Run("non-*string email skips member but does not panic", func(t *testing.T) {
		// userId matches; email is the wrong type. The matching member is
		// dropped from the result but the call must not panic.
		c := makeChat(&chatID, makeMember(&u1, "not-a-pointer"), makeMember(&u2, &e2))
		var got *clientmodels.Chat
		assert.NotPanics(t, func() { got = checkGroupChat(c, []string{"u1", "u2"}) })
		assert.NotNil(t, got)
		assert.Len(t, got.Members, 1)
		assert.Equal(t, u2, got.Members[0].UserID)
	})

	t.Run("nil *string userId is skipped", func(t *testing.T) {
		var nilStr *string
		c := makeChat(&chatID, makeMember(nilStr, &e1), makeMember(&u2, &e2))
		var got *clientmodels.Chat
		assert.NotPanics(t, func() { got = checkGroupChat(c, []string{"u1", "u2"}) })
		assert.Nil(t, got)
	})
}

func TestGetAppReturnsErrorWhenNotConnected(t *testing.T) {
	// A client whose Connect() failed (or was never called) has a nil Graph
	// client. GetApp must return an error rather than panicking on the nil
	// dereference (regression guard for the credentials-check job crash).
	c := &ClientImpl{}
	app, err := c.GetApp("some-app-id")
	assert.Nil(t, app)
	assert.Error(t, err)
}

// newCatalogTestClient serves /appCatalogs/teamsApps from catalog, keyed by the
// $filter Graph receives, or fails every request when errStatus is non-zero. It
// records each filter in the order it was sent.
func newCatalogTestClient(t *testing.T, catalog map[string]string, errStatus int) (*ClientImpl, *[]string) {
	t.Helper()

	var filters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter := r.URL.Query().Get("$filter")
		filters = append(filters, filter)
		w.Header().Set("Content-Type", "application/json")

		if errStatus != 0 {
			w.WriteHeader(errStatus)
			_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"denied","innerError":{"request-id":"r1"}}}`))
			return
		}

		body := `{"value":[]}`
		if id, ok := catalog[filter]; ok {
			body = fmt.Sprintf(`{"value":[{"id":%q}]}`, id)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	adapter, err := msgraphsdk.NewGraphRequestAdapter(&authentication.AnonymousAuthenticationProvider{})
	require.NoError(t, err)
	adapter.SetBaseUrl(server.URL)

	return &ClientImpl{client: msgraphsdk.NewGraphServiceClient(adapter), ctx: context.Background()}, &filters
}

func TestGetTeamsAppIDByExternalID(t *testing.T) {
	const manifestID = "1dfa4d4c-62a4-4a92-b8e4-4a2b0a1b5e10"
	byExternalID := "externalId eq '" + manifestID + "'"
	byCatalogID := "id eq '" + manifestID + "'"

	t.Run("organization app matches by externalId", func(t *testing.T) {
		client, filters := newCatalogTestClient(t, map[string]string{byExternalID: "catalog-id"}, 0)

		appID, err := client.GetTeamsAppIDByExternalID(manifestID)
		require.NoError(t, err)
		assert.Equal(t, "catalog-id", appID)
		assert.Equal(t, []string{byExternalID}, *filters)
	})

	t.Run("store app falls back to the catalog id", func(t *testing.T) {
		client, filters := newCatalogTestClient(t, map[string]string{byCatalogID: manifestID}, 0)

		appID, err := client.GetTeamsAppIDByExternalID(manifestID)
		require.NoError(t, err)
		assert.Equal(t, manifestID, appID)
		assert.Equal(t, []string{byExternalID, byCatalogID}, *filters)
	})

	t.Run("no match on either field", func(t *testing.T) {
		client, filters := newCatalogTestClient(t, nil, 0)

		_, err := client.GetTeamsAppIDByExternalID(manifestID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), manifestID)
		assert.Equal(t, []string{byExternalID, byCatalogID}, *filters)
	})

	t.Run("a Graph error stops the lookup", func(t *testing.T) {
		client, filters := newCatalogTestClient(t, nil, http.StatusForbidden)

		_, err := client.GetTeamsAppIDByExternalID(manifestID)
		require.Error(t, err)
		assert.Equal(t, []string{byExternalID}, *filters)
	})
}
