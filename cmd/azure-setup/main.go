// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"os"
	"time"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-plugin-ms-embedded/server/cloudenv"
)

var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		progressf("Error: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "azure-setup",
	Short: "Automate Azure AD setup for Mattermost Embedded plugin",
	Long: `Azure Setup Tool for Mattermost Embedded

This tool automates the Azure AD application registration and configuration
process required for the Mattermost Embedded plugin. It handles:

  • App registration creation/update
  • API permissions configuration
  • Application ID URI and scope setup
  • Pre-authorized client applications
  • Client secret generation
  • Auditing an existing registration with the doctor command

The tool requires an authenticated Azure account with permissions to manage
applications in your Azure AD tenant.`,
	Version: version,

	// main already prints the error, so cobra must not print it a second time.
	// Usage is deliberately left enabled here: a missing or misspelled flag is a
	// usage mistake and the flag list helps. Commands that can fail for
	// non-usage reasons silence it themselves once their flags have parsed.
	SilenceErrors: true,
}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create or update Azure AD application registration",
	Long: `Create a new Azure AD application or update an existing one with the
required configuration for the Mattermost Embedded plugin.

This command will:
  1. Authenticate to Azure
  2. Validate your permissions
  3. Create or update the application
  4. Configure API permissions (User.Read, TeamsActivity.Send, AppCatalog.Read.All)
  5. Set up API exposure with access_as_user scope
  6. Add pre-authorized Microsoft clients (Teams, Outlook)
  7. Generate a client secret

Pass --create-doctor-requirements to additionally request the read-only Graph
permissions that the doctor command needs.

Example:
  azure-setup create --site-url https://mattermost.example.com --app-name "Mattermost for Teams"
  azure-setup create --site-url https://mm.example.com --client-id abc123... --dry-run`,
	RunE: runCreate,
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate Azure credentials and permissions",
	Long: `Validate that you can authenticate to Azure and have the necessary
permissions to create and manage applications.

This command performs a dry-run check without making any changes.`,
	RunE: runValidate,
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Audit an existing Azure AD application and report on its configuration",
	Long: `Inspect an existing Azure AD application registration and report whether every
setting the Mattermost Embedded plugin depends on is configured correctly.

The doctor makes no changes. It reads the application, its service principal, and
the consent grants in the tenant, and checks:

  1. The signed-in identity and its application administration roles
  2. The application exists, is single tenant, and has the expected Application ID URI
  3. The access_as_user scope is exposed, enabled, and user-consentable
  4. Every Microsoft first-party client (Teams, Outlook, Office, Copilot) is pre-authorized
  5. The required Graph permissions are requested
  6. A service principal exists and admin consent has been granted for each permission
  7. At least one client secret is valid, with a warning before it expires
  8. Housekeeping: application owners and duplicate registrations sharing the name

The command exits non-zero when any check fails.

Example:
  azure-setup doctor --client-id abc123... --site-url https://mattermost.example.com
  azure-setup doctor --app-name "Mattermost for Teams" -o markdown --report-file report.md`,
	RunE: runDoctor,
}

// Command flags
var (
	flagTenantID          string
	flagSiteURL           string
	flagAppName           string
	flagClientID          string
	flagSecretExpiration  int
	flagDryRun            bool
	flagNonInteractive    bool
	flagVerbose           bool
	flagOutputFormat      string
	flagYes               bool
	flagCloud             string
	flagDoctorReqs        bool
	flagReportFile        string
	flagSecretWarningDays int
)

func init() {
	// Create command flags
	createCmd.Flags().StringVar(&flagTenantID, "tenant-id", "", "Azure AD Tenant ID (optional)")
	createCmd.Flags().StringVar(&flagSiteURL, "site-url", "", "Mattermost site URL (required)")
	createCmd.Flags().StringVar(&flagAppName, "app-name", "Mattermost for Teams", "Application display name")
	createCmd.Flags().StringVar(&flagClientID, "client-id", "", "Existing application client ID (for updates)")
	createCmd.Flags().IntVar(&flagSecretExpiration, "secret-expiration", 12, "Client secret expiration in months (1-24)")
	createCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview changes without applying them")
	createCmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Run in non-interactive mode")
	createCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose output")
	createCmd.Flags().StringVarP(&flagOutputFormat, "output", "o", "human", "Output format (human, json, env, mattermost)")
	createCmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompt and proceed with changes")
	createCmd.Flags().StringVar(&flagCloud, "cloud", cloudenv.Commercial, "Microsoft national cloud (commercial, gcchigh, dod)")
	createCmd.Flags().BoolVar(&flagDoctorReqs, "create-doctor-requirements", false, "Also request the read-only Graph permissions the doctor command needs")

	_ = createCmd.MarkFlagRequired("site-url")

	// Validate command flags
	validateCmd.Flags().StringVar(&flagTenantID, "tenant-id", "", "Azure AD Tenant ID (optional)")
	validateCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose output")
	validateCmd.Flags().StringVar(&flagCloud, "cloud", cloudenv.Commercial, "Microsoft national cloud (commercial, gcchigh, dod)")

	// Doctor command flags
	doctorCmd.Flags().StringVar(&flagTenantID, "tenant-id", "", "Azure AD Tenant ID (optional)")
	doctorCmd.Flags().StringVar(&flagClientID, "client-id", "", "Client ID of the application to inspect (preferred over --app-name)")
	doctorCmd.Flags().StringVar(&flagAppName, "app-name", "Mattermost for Teams", "Application display name to look up when --client-id is not given")
	doctorCmd.Flags().StringVar(&flagSiteURL, "site-url", "", "Mattermost site URL, used to verify the Application ID URI")
	doctorCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose output")
	doctorCmd.Flags().StringVarP(&flagOutputFormat, "output", "o", "human", "Report format (human, json, markdown)")
	doctorCmd.Flags().StringVar(&flagReportFile, "report-file", "", "Also write the report to this file")
	doctorCmd.Flags().IntVar(&flagSecretWarningDays, "secret-warning-days", DefaultSecretWarningDays, "Warn when a client secret expires within this many days")
	doctorCmd.Flags().StringVar(&flagCloud, "cloud", cloudenv.Commercial, "Microsoft national cloud (commercial, gcchigh, dod)")

	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(doctorCmd)
}

// runCreate executes the create command
func runCreate(cmd *cobra.Command, args []string) error {
	// Flags have parsed by the time RunE is reached, so a failure from here on
	// is an Azure or configuration error rather than a usage mistake.
	cmd.SilenceUsage = true

	// Set a reasonable timeout for the entire operation
	// 10 minutes allows sufficient time for device code flows with MFA
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Build configuration
	config := &SetupConfig{
		CreateDoctorRequirements: flagDoctorReqs,

		TenantID:          flagTenantID,
		MattermostSiteURL: flagSiteURL,
		AppName:           flagAppName,
		ClientID:          flagClientID,
		SecretExpiration:  flagSecretExpiration,
		DryRun:            flagDryRun,
		NonInteractive:    flagNonInteractive,
		Verbose:           flagVerbose,
		OutputFormat:      flagOutputFormat,
		SkipConfirmation:  flagYes,
		Cloud:             flagCloud,
		ctx:               ctx,
		rollback:          []func() error{},
	}

	// Validate inputs
	if err := validateInputs(config); err != nil {
		return errors.Wrap(err, "invalid input")
	}

	env := config.cloudEnvironment()

	// Authenticate to Azure
	cred, err := authenticateToAzure(ctx, env, config.TenantID, config.Verbose)
	if err != nil {
		return errors.Wrap(err, "authentication failed")
	}
	config.credential = cred

	// Validate Azure connection
	if err = validateAzureConnection(ctx, env, cred, config.Verbose); err != nil {
		return errors.Wrap(err, "Azure connection validation failed")
	}

	client, err := newGraphClient(env, cred)
	if err != nil {
		return err
	}

	// Validate permissions
	if err = validatePermissions(ctx, client, config.Verbose); err != nil {
		return errors.Wrap(err, "permission validation failed")
	}

	// Check for existing app
	existingApp, err := checkExistingApp(ctx, client, config.AppName, config.ClientID, config.Verbose)
	if err != nil {
		return errors.Wrap(err, "failed to check for existing application")
	}

	// Resolve the permission set once, before the operator is asked to approve
	// it, so the confirmation cannot list something different from what gets
	// requested.
	permissions, err := resolvePermissions(ctx, client, config)
	if err != nil {
		return err
	}

	// Show pre-flight confirmation unless skipped
	if !config.DryRun && !config.SkipConfirmation && !config.NonInteractive {
		if err = showPreflightConfirmation(config, existingApp, permissions); err != nil {
			return err
		}
	}

	// Execute setup with rollback on error
	result, err := executeSetup(ctx, client, config, existingApp, permissions)
	if err != nil {
		if !config.DryRun {
			executeRollback(config)
		}
		return err
	}

	// Output results
	return OutputResult(result, config.OutputFormat)
}

// runValidate executes the validate command
func runValidate(cmd *cobra.Command, args []string) error {
	// See runCreate: past flag parsing, a failure is not a usage mistake.
	cmd.SilenceUsage = true

	// Set a reasonable timeout for validation
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	progressln("🔍 Validating Azure credentials and permissions...")

	env, err := resolveCloud(flagCloud)
	if err != nil {
		return errors.Wrap(err, "invalid input")
	}

	// Authenticate to Azure
	cred, err := authenticateToAzure(ctx, env, flagTenantID, flagVerbose)
	if err != nil {
		return errors.Wrap(err, "authentication failed")
	}

	// Validate Azure connection
	if err = validateAzureConnection(ctx, env, cred, flagVerbose); err != nil {
		return errors.Wrap(err, "Azure connection validation failed")
	}

	client, err := newGraphClient(env, cred)
	if err != nil {
		return err
	}

	// Validate permissions
	if err = validatePermissions(ctx, client, flagVerbose); err != nil {
		return errors.Wrap(err, "permission validation failed")
	}

	progressln("\n✅ Validation complete - you are ready to create applications")
	return nil
}

// executeSetup orchestrates the entire setup process
func executeSetup(ctx context.Context, client *msgraphsdk.GraphServiceClient, config *SetupConfig, existingApp models.Applicationable, permissions []requiredPermission) (*SetupResult, error) {
	var app models.Applicationable
	var created bool
	var err error

	// Create or update application
	app, created, err = createOrUpdateApp(ctx, client, config, existingApp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create/update application")
	}

	// Configure API permissions
	if err = configureAPIPermissions(ctx, client, config, app, permissions); err != nil {
		return nil, errors.Wrap(err, "failed to configure API permissions")
	}

	// Configure API exposure
	if err = configureAPIExposure(ctx, client, config, app); err != nil {
		return nil, errors.Wrap(err, "failed to configure API exposure")
	}

	// Generate client secret
	var clientSecret string
	var secretExpiration time.Time
	if !config.DryRun {
		clientSecret, secretExpiration, err = generateClientSecret(ctx, client, config, app)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate client secret")
		}
	} else {
		secretExpiration = time.Now().AddDate(0, config.SecretExpiration, 0)
		clientSecret = "***DRY-RUN-NO-SECRET-GENERATED***" // #nosec G101 -- This is a placeholder for dry-run mode, not a real credential
	}

	// Build application ID URI
	appIDURI, err := buildApplicationIDURI(config.MattermostSiteURL, *app.GetAppId())
	if err != nil {
		return nil, errors.Wrap(err, "failed to build Application ID URI")
	}

	// Get tenant ID - either from config or from Azure
	tenantID := config.TenantID
	if tenantID == "" {
		// Try to get tenant ID from the organization
		tenantID, err = getTenantID(ctx, client, config.Verbose)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get tenant ID - please provide --tenant-id flag")
		}
	}

	// Build result
	result := &SetupResult{
		DoctorRequirements:  config.CreateDoctorRequirements,
		Success:             true,
		Message:             "Setup completed successfully",
		ApplicationClientID: *app.GetAppId(),
		TenantID:            tenantID,
		ClientSecret:        clientSecret,
		SecretExpiration:    secretExpiration.Format("2006-01-02 15:04:05 MST"),
		ApplicationID:       *app.GetId(),
		ApplicationName:     *app.GetDisplayName(),
		ApplicationIDURI:    appIDURI,
		PortalHost:          config.cloudEnvironment().PortalHost,
		Created:             created,
		DryRun:              config.DryRun,
	}

	return result, nil
}
