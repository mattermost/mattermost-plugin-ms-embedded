// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package assets

import (
	"embed"
)

//go:embed *.tmpl
var Templates embed.FS

//go:embed mm-logo-color.png
var LogoColorData []byte

//go:embed mm-logo-outline.png
var LogoOutlineData []byte

//go:embed iframe.html.tmpl
var IFrameHTMLTemplate string

//go:embed iframe_notification_preview.html.tmpl
var IFrameNotificationPreviewHTMLTemplate string

//go:embed sso_wait.html.tmpl
var SSOWaitHTMLTemplate string

//go:embed sso_complete.html.tmpl
var SSOCompleteHTMLTemplate string

//go:embed appmanifest.json.tmpl
var AppManifestTemplate string
