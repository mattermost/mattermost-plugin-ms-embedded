package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/sirupsen/logrus"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/msteams"
	"github.com/mattermost/mattermost-plugin-ms-embedded/server/store/pluginstore"
)

const (
	pluginID                = "com.mattermost.ms-embedded"
	checkCredentialsJobName = "check_credentials" //#nosec G101 -- This is a false positive
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// client is the Mattermost server API client.
	client *pluginapi.Client

	// msteamsAppClient is the client used to communicate with the Microsoft Teams API.
	msteamsAppClient      msteams.Client
	msteamsAppClientMutex sync.RWMutex

	// appIDLookupLock serializes on-demand Teams app ID lookups; appIDLookupFailedAt throttles retries after a failure.
	appIDLookupLock     sync.Mutex
	appIDLookupFailedAt time.Time

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	// handlers for incoming Rest API requests
	apiHandler *API

	// plugin KV store
	pluginStore pluginstore.Store

	// tabAppJWTKeyFunc is the keyfunc.Keyfunc used to verify JWTs from Microsoft Teams
	tabAppJWTKeyFunc  keyfunc.Keyfunc
	cancelKeyFunc     context.CancelFunc
	cancelKeyFuncLock sync.Mutex
	// activeJWKSURL is the JWKS URL the current tabAppJWTKeyFunc was built for.
	// It is guarded by cancelKeyFuncLock and used to detect when a national_cloud
	// change requires rebuilding the keyfunc against a different authority.
	activeJWKSURL string

	// checkCredentialsJob is a job that periodically checks credentials and permissions against the MS Graph API
	checkCredentialsJob     *cluster.Job
	disableCheckCredentials bool

	// clientReconnectCtx and clientReconnectCancel are used to control the client reconnection goroutine
	clientReconnectCtx    context.Context
	clientReconnectCancel context.CancelFunc
	clientReconnectLock   sync.Mutex
}

func (p *Plugin) GetClientForApp() msteams.Client {
	p.msteamsAppClientMutex.RLock()
	defer p.msteamsAppClientMutex.RUnlock()

	return p.msteamsAppClient
}

// OnActivate is invoked when the plugin is activated. If an error is returned, the plugin will be deactivated.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	logger := logrus.StandardLogger()
	pluginapi.ConfigureLogrus(logger, p.client)

	config := p.client.Configuration.GetConfig()
	license := p.client.System.GetLicense()
	if !pluginapi.IsE20LicensedOrDevelopment(config, license) {
		return errors.New("this plugin requires an enterprise license")
	}

	// Configure frame ancestors for the current cloud. This runs before
	// pluginStore is set below so the resulting SaveConfig (which fires
	// OnConfigurationChange) does not trigger a restart. A later national_cloud
	// change is handled in OnConfigurationChange.
	if err := p.updateFrameAncestors(); err != nil {
		p.API.LogWarn("Failed to update frame ancestors", "error", err.Error())
		// Continue activation even if this fails.
	}

	p.apiHandler = NewAPI(p)

	p.pluginStore = pluginstore.NewPluginStore(p.API)

	go p.start(false)

	return nil
}

// updateFrameAncestors adds the configured national cloud's embedding domains to
// ServiceSettings.FrameAncestors, preserving any existing values.
//
// This is intentionally union-only: it adds the current cloud's domains but does
// not prune domains previously added for another cloud. ServiceSettings.FrameAncestors
// is shared, server-wide configuration that administrators and other plugins may
// also modify, and there is no reliable way to tell which entries this plugin
// owns, so pruning risks removing domains that are legitimately configured. A
// leftover domain from a previously selected cloud is a benign over-permission
// (it only permits embedding in a Microsoft cloud the tenant is not using) and
// does not affect the selected cloud, so the union approach is preferred over
// risky removal.
func (p *Plugin) updateFrameAncestors() error {
	p.API.LogDebug("Updating frame ancestors configuration")

	// Get the current Mattermost configuration
	config := p.client.Configuration.GetConfig()
	if config == nil {
		return errors.New("failed to get Mattermost configuration")
	}

	// Get the current frame ancestors as a space-separated string
	currentAncestorsStr := ""
	if config.ServiceSettings.FrameAncestors != nil {
		currentAncestorsStr = *config.ServiceSettings.FrameAncestors
	}

	// Split the current ancestors into a slice
	var currentAncestors []string
	if currentAncestorsStr != "" {
		currentAncestors = strings.Fields(currentAncestorsStr)
	}

	// Parse the allowed frame ancestors for the configured national cloud.
	allowedDomains := p.getConfiguration().CloudEnvironment().FrameAncestors

	// Create a map to track unique domains and preserve existing ones
	uniqueDomains := make(map[string]bool)

	// Track if any new domains were added
	domainsAdded := false

	// Add existing domains to the map
	for _, domain := range currentAncestors {
		uniqueDomains[domain] = true
	}

	// Add our allowed domains to the map, tracking if any new ones were added
	for _, domain := range allowedDomains {
		if !uniqueDomains[domain] {
			domainsAdded = true
			uniqueDomains[domain] = true
		}
	}

	// Only proceed with update if domains were added
	if !domainsAdded {
		p.API.LogDebug("No new domains to add to frame ancestors, skipping update")
		return nil
	}

	// Convert the map back to a slice
	newAncestors := make([]string, 0, len(uniqueDomains))
	for domain := range uniqueDomains {
		newAncestors = append(newAncestors, domain)
	}

	// Sort the slice alphabetically
	sort.Strings(newAncestors)

	// Join the slice into a space-separated string
	newAncestorsStr := strings.Join(newAncestors, " ")

	// Update the configuration
	config.ServiceSettings.FrameAncestors = &newAncestorsStr

	// Save the updated configuration
	err := p.client.Configuration.SaveConfig(config)
	if err != nil {
		return errors.New("failed to save updated frame ancestors configuration: " + err.Error())
	}
	return nil
}

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	p.stop(false)
	return nil
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.apiHandler.ServeHTTP(w, r)
}

// ensureJWKS makes sure the JWKS keyfunc used to verify Microsoft Teams JWTs
// matches the JWKS authority of the configured national cloud. It builds the
// keyfunc on first use and rebuilds it if the configured cloud's JWKS URL
// changes (for example when national_cloud is switched from commercial to
// gcchigh after the plugin was first activated on the commercial default). When
// the URL is unchanged it does nothing, preserving the common no-change case.
func (p *Plugin) ensureJWKS() {
	jwksURL := p.getConfiguration().CloudEnvironment().JWKSURL

	p.cancelKeyFuncLock.Lock()
	defer p.cancelKeyFuncLock.Unlock()

	if p.cancelKeyFunc != nil && p.activeJWKSURL == jwksURL {
		return
	}

	// Tear down the previous keyfunc (if any) before building the new one so we
	// do not leak its background refresh goroutine.
	if p.cancelKeyFunc != nil {
		p.cancelKeyFunc()
		p.cancelKeyFunc = nil
		p.tabAppJWTKeyFunc = nil
	}

	p.tabAppJWTKeyFunc, p.cancelKeyFunc = jwksSetup(jwksURL)
	p.activeJWKSURL = jwksURL
}

func (p *Plugin) start(isRestart bool) {
	// Set up (or rebuild) the JWK set used to verify JWTs from Microsoft Teams.
	// This runs on every start, including restarts, so that changing the
	// national_cloud setting switches to the new cloud's JWKS authority.
	p.ensureJWKS()

	// Initialize context for client reconnection
	p.clientReconnectLock.Lock()
	if p.clientReconnectCtx == nil {
		p.clientReconnectCtx, p.clientReconnectCancel = context.WithCancel(context.Background())
	}
	p.clientReconnectLock.Unlock()

	// Without M365 credentials there is nothing to connect to. This is the
	// expected state for a freshly installed, not-yet-configured plugin, so we
	// skip the connection (and the credentials check) rather than logging
	// connection errors. A later OnConfigurationChange restarts the plugin once
	// credentials are set. Only log on the initial start (not on restarts) to
	// avoid duplicate messages, matching the JWKS setup guard above.
	if !p.getConfiguration().isM365Configured() {
		if !isRestart {
			p.API.LogInfo("MS Teams integration is not configured yet; set the M365 tenant ID, client ID, and client secret in the System Console to enable it")
		}
		return
	}

	// connect to the Microsoft Teams API
	err := p.connectTeamsAppClient()
	if err != nil {
		p.API.LogError("Plugin startup failed: unable to connect Teams app client", "error", err)
		return
	}

	if !p.getConfiguration().DisableCheckCredentials {
		checkCredentialsJob, jobErr := cluster.Schedule(
			p.API,
			checkCredentialsJobName,
			cluster.MakeWaitForRoundedInterval(24*time.Hour),
			p.checkCredentials,
		)
		if jobErr != nil {
			p.API.LogError("Plugin startup failed: error scheduling check credentials job", "error", jobErr)
			return
		}
		p.checkCredentialsJob = checkCredentialsJob

		// Run the job above right away
		go p.checkCredentials()
	}

	p.API.LogDebug("plugin started")
}

func (p *Plugin) stop(isRestart bool) {
	if p.checkCredentialsJob != nil {
		if err := p.checkCredentialsJob.Close(); err != nil {
			p.API.LogError("Failed to close background check credentials job", "error", err)
		}
		p.checkCredentialsJob = nil
	}

	// Clean up the Teams app client so it gets recreated on restart
	p.msteamsAppClientMutex.Lock()
	p.msteamsAppClient = nil
	p.msteamsAppClientMutex.Unlock()

	// Cancel the client reconnection context if not restarting
	if !isRestart {
		p.clientReconnectLock.Lock()
		if p.clientReconnectCancel != nil {
			p.clientReconnectCancel()
			p.clientReconnectCtx = nil
			p.clientReconnectCancel = nil
		}
		p.clientReconnectLock.Unlock()

		p.cancelKeyFuncLock.Lock()
		if p.cancelKeyFunc != nil {
			p.cancelKeyFunc()
			p.cancelKeyFunc = nil
		}
		p.activeJWKSURL = ""
		p.cancelKeyFuncLock.Unlock()
	}
}

func (p *Plugin) restart() {
	p.stop(true)
	p.start(true)
}

const appIDLookupRetryInterval = time.Minute

// refreshTeamsAppID looks up the Teams app catalog ID, stores it, and returns it.
func (p *Plugin) refreshTeamsAppID(client msteams.Client) (string, error) {
	appID, err := client.GetTeamsAppIDByExternalID(p.getConfiguration().AppID)
	if err != nil {
		return "", fmt.Errorf("failed to look up Teams application ID: %w", err)
	}

	if err := p.pluginStore.StoreAppID(client.GetTenantID(), appID); err != nil {
		return "", fmt.Errorf("failed to store Teams application ID: %w", err)
	}

	p.API.LogDebug("Retrieved Teams internal application ID", "appID", appID)
	return appID, nil
}

// getTeamsAppID returns the stored Teams app catalog ID. When none is stored,
// it looks the ID up again, because the lookup at connect time can fail.
func (p *Plugin) getTeamsAppID() (string, error) {
	client := p.GetClientForApp()
	if client == nil {
		return "", errors.New("app client is not connected")
	}

	appID, err := p.pluginStore.GetAppID(client.GetTenantID())
	if err == nil {
		return appID, nil
	}
	if !pluginstore.IsErrNotFound(err) {
		return "", err
	}

	p.appIDLookupLock.Lock()
	defer p.appIDLookupLock.Unlock()

	if appID, err = p.pluginStore.GetAppID(client.GetTenantID()); err == nil {
		return appID, nil
	}
	if time.Since(p.appIDLookupFailedAt) < appIDLookupRetryInterval {
		return "", errors.New("teams application ID lookup failed recently, not retrying yet")
	}

	appID, err = p.refreshTeamsAppID(client)
	if err != nil {
		p.appIDLookupFailedAt = time.Now()
		return "", err
	}

	return appID, nil
}

func (p *Plugin) connectTeamsAppClient() error {
	p.msteamsAppClientMutex.Lock()
	defer p.msteamsAppClientMutex.Unlock()

	// We don't currently support reconnecting with a new configuration: a plugin restart is
	// required.
	if p.msteamsAppClient != nil {
		return nil
	}

	p.msteamsAppClient = msteams.NewApp(
		p.getConfiguration().CloudEnvironment(),
		p.getConfiguration().M365TenantID,
		p.getConfiguration().M365ClientID,
		p.getConfiguration().M365ClientSecret,
		&p.client.Log,
	)

	err := p.msteamsAppClient.Connect()
	if err != nil {
		// Connect failed, so the client is only partially initialized (its
		// internal Graph client is nil). Clear it so GetClientForApp reports
		// no client rather than handing out an unusable one, and so a later
		// attempt (for example after credentials are configured) retries.
		p.msteamsAppClient = nil
		p.API.LogError("Unable to connect to the app client", "error", err)
		return err
	}

	if p.getConfiguration().M365ClientID != "" {
		// The ID is required for activity feed notifications only. A failure is logged and retried on demand.
		if _, err := p.refreshTeamsAppID(p.msteamsAppClient); err != nil {
			p.API.LogError("Unable to retrieve Teams application ID", "error", err)
		}
	}

	// Get a local copy of the context to use in the goroutine
	p.clientReconnectLock.Lock()
	ctx := p.clientReconnectCtx
	p.clientReconnectLock.Unlock()

	// If the reconnection context is gone, the plugin is stopping (a concurrent
	// stop() cleared it). Skip starting the goroutine rather than dereferencing a
	// nil context in the select below.
	if ctx == nil {
		return nil
	}

	// Start a goroutine to periodically reconnect the client to refresh the token
	go func(ctx context.Context) {
		p.API.LogDebug("Starting client reconnection goroutine")

		reconnectInterval := 12 * time.Hour
		ticker := time.NewTicker(reconnectInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				p.API.LogDebug("Client reconnection goroutine stopped")
				return
			case <-ticker.C:
				p.API.LogDebug("Reconnecting MS Teams app client to refresh token")
				p.msteamsAppClientMutex.Lock()
				if p.msteamsAppClient != nil {
					if err := p.msteamsAppClient.Connect(); err != nil {
						p.API.LogError("Failed to reconnect MS Teams app client", "error", err)
					} else {
						p.API.LogDebug("Successfully reconnected MS Teams app client")
					}
				}
				p.msteamsAppClientMutex.Unlock()
			}
		}
	}(ctx)

	return nil
}
