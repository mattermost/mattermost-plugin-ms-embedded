// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	pluginapi "github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/v8/channels/utils"
	"github.com/sirupsen/logrus"

	"github.com/mattermost/mattermost-plugin-ms-embedded/assets"
	"github.com/mattermost/mattermost-plugin-ms-embedded/server/store/pluginstore"
)

const (
	// TeamsJSVersion and TeamsJSIntegrity are the single source of truth for the
	// Microsoft Teams JS SDK loaded by iframe and SSO HTML templates. Bump both together.
	TeamsJSVersion   = "2.53.0"
	TeamsJSIntegrity = "sha384-UFKwOGC8ix6vOFFC4vH8hSpjwkQXZmSjCx8aaxwhbtm+6joQdNvD7b4pPk82cYAD"
)

type iFrameContext struct {
	SiteURL    string
	PluginID   string
	TenantID   string
	TeamsAppID string
	UserID     string
	Nonce      string

	TeamsJSVersion string
	// TeamsJSIntegrityAttr is the full integrity="..." attribute. Injected as
	// template.HTMLAttr so html/template does not escape '+' in the SRI hash to '&#43;'.
	TeamsJSIntegrityAttr template.HTMLAttr

	Post                       *model.Post
	PostJSON                   string
	NotificationPreviewContext iFrameNotificationPreviewContext

	CSPScriptSrc  string
	CSPConnectSrc string
	CSPFrameSrc   string
}

type iFrameNotificationPreviewContext struct {
	PostAuthor *model.User
	Channel    *model.Channel

	ChannelNameDisplay   string
	PostAuthorDisplay    string
	PostCreatedAtDisplay string
	Action               string
}

// iFrame returns the iFrame HTML needed to host Mattermost within a MS Teams app.
func (a *API) iFrame(w http.ResponseWriter, r *http.Request) {
	a.p.API.LogDebug("iFrame", "action", r.URL.Query().Get("action"), "sub_entity_id", r.URL.Query().Get("sub_entity_id"))

	iFrameCtx, err := a.createIFrameContext("", nil)
	if err != nil {
		a.p.API.LogError("Failed to create iFrame context", "error", err.Error())
		http.Error(w, "Failed to create iFrame context", http.StatusInternalServerError)
		return
	}

	html, err := a.formatTemplate(assets.IFrameHTMLTemplate, iFrameCtx)
	if err != nil {
		a.p.API.LogError("Failed to format iFrame HTML", "error", err.Error())
		http.Error(w, "Failed to format iFrame HTML", http.StatusInternalServerError)
		return
	}

	iFrameCtx.CSPScriptSrc = "https://res.cdn.office.net"
	iFrameCtx.CSPConnectSrc = a.p.getConfiguration().CloudEnvironment().CSPConnectSrc
	iFrameCtx.CSPFrameSrc = "self"

	a.returnCSPHeaders(w, iFrameCtx)
	w.Header().Set("Content-Type", "text/html")

	// set session cookie to indicate Mattermost is hosted in an iFrame, which allows
	// webapp to bypass "Where do you want to view this" page and set SameSite=none.
	http.SetCookie(w, &http.Cookie{
		Name:     "MMEMBED",
		Value:    "1",
		Path:     "/",
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	})

	if _, err := w.Write([]byte(html)); err != nil {
		a.p.API.LogWarn("Unable to serve the iFrame", "error", err.Error())
	}
}

func (a *API) iframeNotificationPreview(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")
	if userID == "" {
		a.p.API.LogError("User not authenticated", "user_id", userID)
		http.Error(w, "user not authenticated", http.StatusUnauthorized)
		return
	}
	postID := r.URL.Query().Get("post_id")
	if postID == "" {
		a.p.API.LogError("Request missing post_id", "user_id", userID)
		http.Error(w, "post_id is required", http.StatusBadRequest)
		return
	}

	post, appErr := a.p.API.GetPost(postID)
	if appErr != nil {
		a.p.API.LogError("Failed to get post", "user_id", userID, "post_id", postID, "error", appErr.Error())
		http.Error(w, "failed to get post", http.StatusInternalServerError)
		return
	}

	// Check user has access to the post
	if !a.p.API.HasPermissionToChannel(userID, post.ChannelId, model.PermissionReadChannel) {
		a.p.API.LogError("Blocking notification preview: user does not have access to the post", "user_id", userID, "post_id", postID)
		http.Error(w, "user does not have access to the post", http.StatusForbidden)
		return
	}

	iFrameCtx, err := a.createIFrameContext(userID, post)
	if err != nil {
		a.p.API.LogError("Failed to create iFrame context", "error", err.Error())
		http.Error(w, "Failed to create iFrame context", http.StatusInternalServerError)
		return
	}

	html, err := a.formatTemplate(assets.IFrameNotificationPreviewHTMLTemplate, iFrameCtx)
	if err != nil {
		a.p.API.LogError("Failed to format iFrame HTML", "error", err.Error())
		http.Error(w, "Failed to format iFrame HTML", http.StatusInternalServerError)
		return
	}

	iFrameCtx.CSPConnectSrc = a.p.getConfiguration().CloudEnvironment().CSPConnectSrc
	iFrameCtx.CSPScriptSrc = DefaultCSPScriptSrc
	a.returnCSPHeaders(w, iFrameCtx)
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(html)); err != nil {
		a.p.API.LogError("Unable to serve the iFrame", "error", err.Error())
	}
}

// createIFrameContext creates the iFrame context for the iframe, notification preview, and SSO HTML templates.
func (a *API) createIFrameContext(userID string, post *model.Post) (iFrameContext, error) {
	config := a.p.API.GetConfig()
	if *config.ServiceSettings.SiteURL == "" {
		return iFrameContext{}, fmt.Errorf("ServiceSettings.SiteURL cannot be empty for MS Teams iFrame")
	}

	var appID string
	var err error
	// Get the app ID using the client's tenant ID to ensure consistency with storage
	client := a.p.GetClientForApp()
	if client != nil {
		appID, err = a.p.pluginStore.GetAppID(client.GetTenantID())
		if err != nil {
			a.p.API.LogWarn("Failed to get app ID, button in notification preview won't work", "tenantID", client.GetTenantID(), "error", err.Error())
		}
	}

	iFrameCtx := iFrameContext{
		SiteURL:        *config.ServiceSettings.SiteURL,
		PluginID:       url.PathEscape(manifest.Id),
		TenantID:       a.p.getConfiguration().M365TenantID,
		TeamsAppID:     appID,
		UserID:         userID,
		Post:           post,
		TeamsJSVersion: TeamsJSVersion,
		// TeamsJSIntegrity is a compile-time constant SRI hash, not user input.
		TeamsJSIntegrityAttr: template.HTMLAttr(`integrity="` + TeamsJSIntegrity + `"`), //nolint:gosec // G203
	}

	// Generate a random nonce for the script/style tags
	nonceBytes := make([]byte, 16)
	if _, nonceErr := rand.Read(nonceBytes); nonceErr != nil {
		return iFrameContext{}, fmt.Errorf("failed to generate nonce: %w", nonceErr)
	}
	iFrameCtx.Nonce = base64.StdEncoding.EncodeToString(nonceBytes)

	// If the post is nil, we don't need to do anything else.
	if post == nil {
		return iFrameCtx, nil
	}

	// create context for notification preview
	postJSON, err := json.Marshal(post)
	if err != nil {
		return iFrameContext{}, fmt.Errorf("failed to marshal post: %w", err)
	}
	iFrameCtx.PostJSON = string(postJSON)

	author, appErr := a.p.API.GetUser(post.UserId)
	if appErr != nil {
		return iFrameContext{}, fmt.Errorf("failed to get author: %w", appErr)
	}

	channel, appErr := a.p.API.GetChannel(post.ChannelId)
	if appErr != nil {
		return iFrameContext{}, fmt.Errorf("failed to get channel: %w", appErr)
	}

	channelDisplayName := channel.Name
	action := "mentioned you"
	switch channel.Type {
	case model.ChannelTypeDirect:
		channelDisplayName = "Direct Message"
		action = "sent you a direct message"
	case model.ChannelTypeGroup:
		channelDisplayName = "Group Message"
		action = "sent you a group message"
	}

	iFrameCtx.NotificationPreviewContext = iFrameNotificationPreviewContext{
		PostAuthor:           author,
		Channel:              channel,
		ChannelNameDisplay:   channelDisplayName,
		PostAuthorDisplay:    author.GetDisplayName(model.ShowNicknameFullName),
		PostCreatedAtDisplay: time.Unix(post.CreateAt/1000, 0).Format("January 2, 2006 • 03:04 PM"), // Format date in this way: "April 4, 2025 • 10:43 AM"
		Action:               action,
	}

	return iFrameCtx, nil
}

// formatTemplate formats the iFrame HTML template with the site URL and plugin ID
func (a *API) formatTemplate(templateBody string, iframeCtx iFrameContext) (string, error) {
	tmpl, err := template.New("iFrame").Parse(templateBody)
	if err != nil {
		return "", fmt.Errorf("failed to parse iFrame template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, iframeCtx); err != nil {
		return "", fmt.Errorf("failed to execute iFrame template: %w", err)
	}

	return buf.String(), nil
}

// authenticate expects a Microsoft Entra ID in the Authorization header, and uses that
// to authenticate to the corresponding user in Mattermost, if it exists.
func (a *API) authenticate(w http.ResponseWriter, r *http.Request) {
	var logger logrus.FieldLogger
	logger = logrus.StandardLogger()

	// Check if the user is already logged in *and* if we already have the teams data stored in the kvstore.
	// If we don't have the stored teams data, we allow the flow to continue so we parse the auth token and retrieve the
	// fields we need.
	// If we already have the user in the kvstore, we skip the authentication flow and just redirect the user.
	// If the store retrieval fails, we continue with the logic. If there's an underlying problem with the kvstore it will fail
	// later on.
	if r.Header.Get("Mattermost-User-ID") != "" {
		userID := r.Header.Get("Mattermost-User-ID")
		exist, err := a.p.pluginStore.UserExists(userID)
		if err != nil {
			logger.WithError(err).Error("Failed to check if user exists")
			// Continuing with the logic since exists=false
		}

		if exist {
			logger = logger.WithField("user_id", userID)
			logger.Info("Skipping authentication, user already logged in")

			user, err := a.p.client.User.Get(r.Header.Get("Mattermost-User-ID"))
			if err != nil {
				logger.WithError(err).Error("Failed to get user")
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			http.Redirect(w, r, a.p.getRedirectPathFromUser(logger, user, r.URL.Query().Get("sub_entity_id")), http.StatusSeeOther)
			return
		}
	}

	config := a.p.client.Configuration.GetConfig()

	// Ideally we'd accept the token via an Authorization header, but for now get it from the query string.
	// token := r.Header.Get("Authorization")
	token := r.URL.Query().Get("token")

	// Validate the token in the request, handling all errors if invalid.
	expectedTenantIDs := []string{a.p.getConfiguration().M365TenantID}
	params := &validateTokenParams{
		jwtKeyFunc:          a.p.tabAppJWTKeyFunc,
		token:               token,
		expectedTenantIDs:   expectedTenantIDs,
		skipTokenValidation: shouldSkipTokenValidation(),
		siteURL:             *config.ServiceSettings.SiteURL,
		clientID:            a.p.configuration.M365ClientID,
	}

	claims, validationErr := validateToken(params)
	if validationErr != nil {
		handleErrorWithCode(logger, w, validationErr.StatusCode, validationErr.Message, validationErr.Err)
		return
	}

	if claims == nil {
		handleErrorWithCode(logger, w, http.StatusUnauthorized, "Invalid token claims", nil)
		return
	}

	oid, ok := claims["oid"].(string)
	if !ok || oid == "" {
		logger.Error("Missing or empty claim for oid")
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	logger = logger.WithField("oid", oid)

	ssoUsername, ok := claims["unique_name"].(string)
	if !ok || ssoUsername == "" {
		logger.Warn("Missing or empty claim for unique_name")

		ssoUsername, ok = claims["preferred_username"].(string)
		if !ok || ssoUsername == "" {
			logger.Error("Missing or empty claim for unique_name or preferred_username")
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	}

	mmUser, err := a.p.client.User.GetByEmail(ssoUsername)
	if err != nil && err != pluginapi.ErrNotFound {
		logger.WithError(err).Error("Failed to query Mattermost user matching unique_name")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	} else if mmUser == nil {
		logger.Warn("No Mattermost user matching unique_name, redirecting to login")

		// Redirect to the home page
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	logger = logger.WithField("user_id", mmUser.Id)

	if mmUser.DeleteAt != 0 {
		logger.Warn("Mattermost user is archived, redirecting to login")

		// Redirect to the home page
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Keep track of the unique_name and oid in the user's properties to support
	// notifications in the future.
	storedUser := pluginstore.NewUser(mmUser.Id, oid, ssoUsername)
	err = a.p.pluginStore.StoreUser(storedUser)
	if err != nil {
		logger.WithError(err).Error("Failed to store user")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// If the user was already authenticated we already stored the data we required in the kvstore, so we can safely redirect
	// to the appropriate path without messing with the session and the cookies.
	if r.Header.Get("Mattermost-User-ID") != "" {
		http.Redirect(w, r, a.p.getRedirectPathFromUser(logger, mmUser, r.URL.Query().Get("sub_entity_id")), http.StatusSeeOther)
		return
	}

	// This is effectively copied from https://github.com/mattermost/mattermost/blob/a184e5677d28433495b0cde764bfd99700838740/server/channels/app/login.go#L287
	secure := true
	domain := getCookieDomain(config)
	subpath, _ := utils.GetSubpathFromConfig(config)

	jwtExpiresAt, err := claims.GetExpirationTime()
	if err != nil || jwtExpiresAt == nil {
		logger.WithError(err).Error("Missing or invalid expiration time claim")
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// check if the expiration time is in the past
	if jwtExpiresAt.Before(time.Now()) {
		logger.Error("Token is already expired")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Session expiration is based on the Mattermost server config for web sessions.
	maxAgeSeconds := *config.ServiceSettings.SessionLengthWebInHours * 60 * 60
	expiresAt := time.Now().Add(time.Duration(maxAgeSeconds) * time.Second)

	session, err := a.p.client.Session.Create(&model.Session{
		UserId:    mmUser.Id,
		ExpiresAt: model.GetMillisForTime(expiresAt),
		Roles:     mmUser.GetRawRoles(),
	})
	if err != nil {
		logger.WithError(err).Error("Failed to create session for Mattermost user")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Set session cookie
	sessionCookie := &http.Cookie{
		Name:     model.SessionCookieToken,
		Value:    session.Token,
		Path:     subpath,
		MaxAge:   maxAgeSeconds,
		Expires:  expiresAt,
		HttpOnly: true,
		Domain:   domain,
		Secure:   secure,
		SameSite: http.SameSiteNoneMode,
	}

	userCookie := &http.Cookie{
		Name:     model.SessionCookieUser,
		Value:    mmUser.Id,
		Path:     subpath,
		MaxAge:   maxAgeSeconds,
		Expires:  expiresAt,
		Domain:   domain,
		Secure:   secure,
		SameSite: http.SameSiteNoneMode,
	}

	csrfCookie := &http.Cookie{
		Name:    model.SessionCookieCsrf,
		Value:   session.GetCSRF(),
		Path:    subpath,
		MaxAge:  maxAgeSeconds,
		Expires: expiresAt,
		Domain:  domain,
		Secure:  secure,
	}

	http.SetCookie(w, sessionCookie)
	http.SetCookie(w, userCookie)
	http.SetCookie(w, csrfCookie)

	// Redirect to the home page
	http.Redirect(w, r, a.p.getRedirectPathFromUser(logger, mmUser, r.URL.Query().Get("sub_entity_id")), http.StatusSeeOther)
}

// MessageHasBeenPosted is called when a message is posted in Mattermost. We rely on it to send a user activity notification
// to Microsoft Teams when a user is mentioned in a message.
// This is called in a controller Goroutine in the server side so there's no need to worry about concurrency here.
func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	// Check if user activity notifications are enabled
	if p.getConfiguration().DisableUserActivityNotifications {
		return
	}

	parser := NewNotificationsParser(p.API, p.pluginStore, p.msteamsAppClient, p.getConfiguration().CloudEnvironment().TeamsDomain)
	if err := parser.ProcessPost(post); err != nil {
		p.API.LogError("Failed to process mentions", "error", err.Error())
		return
	}

	if err := parser.SendNotifications(); err != nil {
		p.API.LogError("Failed to send notifications", "error", err.Error())
	}
}

func getCookieDomain(config *model.Config) string {
	if config.ServiceSettings.AllowCookiesForSubdomains != nil && *config.ServiceSettings.AllowCookiesForSubdomains && config.ServiceSettings.SiteURL != nil {
		if siteURL, err := url.Parse(*config.ServiceSettings.SiteURL); err == nil {
			return siteURL.Hostname()
		}
	}
	return ""
}

// getRedirectPathFromUser generates a redirect path for the user based on the subEntityID.
// This is used to redirect the user to the correct URL when they click on a notification in Microsoft Teams.
func (p *Plugin) getRedirectPathFromUser(logger logrus.FieldLogger, user *model.User, subEntityID string) string {
	if subEntityID != "" {
		if postID, ok := strings.CutPrefix(subEntityID, "post_preview_"); ok {
			return fmt.Sprintf("/plugins/%s/iframe/notification_preview?post_id=%s", url.PathEscape(manifest.Id), url.QueryEscape(postID))
		} else if strings.HasPrefix(subEntityID, "post_") {
			var team *model.Team
			postID := strings.TrimPrefix(subEntityID, "post_")
			post, appErr := p.API.GetPost(postID)
			if appErr != nil {
				logger.WithError(appErr).Error("Failed to get post to generate redirect path from subEntityId")
				return "/"
			}

			channel, appErr := p.API.GetChannel(post.ChannelId)
			if appErr != nil {
				logger.WithError(appErr).Error("Failed to get channel to generate redirect path from subEntityId")
				return "/"
			}

			if channel.TeamId == "" {
				var teams []*model.Team
				teams, appErr = p.API.GetTeamsForUser(user.Id)
				if appErr != nil {
					logger.WithError(appErr).Error("Failed to get teams for user to generate redirect path from subEntityId")
					return "/"
				}
				if len(teams) == 0 {
					logger.Warn("User has no teams to generate redirect path from subEntityId")
					return "/"
				}
				team = teams[0]
			} else {
				team, appErr = p.API.GetTeam(channel.TeamId)
				if appErr != nil {
					logger.WithError(appErr).Error("Failed to get team to generate redirect path from subEntityId")
					return "/"
				}
			}

			return fmt.Sprintf("/%s/pl/%s", url.PathEscape(team.Name), url.PathEscape(post.Id))
		}
	}

	return "/"
}
