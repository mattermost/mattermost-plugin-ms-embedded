// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
)

func TestProcessPostNilChannel(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetChannel", "channel-id").Return(nil, nil)

	parser := NewNotificationsParser(api, nil, nil, "")
	err := parser.ProcessPost(&model.Post{ChannelId: "channel-id", Message: "hello"})

	assert.ErrorContains(t, err, "channel-id")
}
