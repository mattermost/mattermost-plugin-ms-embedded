// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateConfigurationAppNameLength(t *testing.T) {
	p := &Plugin{}
	config := func(name string) *configuration {
		return &configuration{M365TenantID: "t", M365ClientID: "c", M365ClientSecret: "s", AppVersion: "1.0.0", AppID: "id", AppName: name}
	}

	assert.NoError(t, p.validateConfiguration(config(strings.Repeat("a", AppNameMaxLength))))
	assert.NoError(t, p.validateConfiguration(config(strings.Repeat("é", AppNameMaxLength))))
	assert.ErrorContains(t, p.validateConfiguration(config(strings.Repeat("a", AppNameMaxLength+1))), "longer than 30")
}

func TestIsM365Configured(t *testing.T) {
	tests := []struct {
		name   string
		config configuration
		want   bool
	}{
		{"all set", configuration{M365TenantID: "t", M365ClientID: "c", M365ClientSecret: "s"}, true},
		{"all empty", configuration{}, false},
		{"missing tenant", configuration{M365ClientID: "c", M365ClientSecret: "s"}, false},
		{"missing client id", configuration{M365TenantID: "t", M365ClientSecret: "s"}, false},
		{"missing secret", configuration{M365TenantID: "t", M365ClientID: "c"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.config.isM365Configured())
		})
	}
}
