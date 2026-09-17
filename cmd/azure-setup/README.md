# Azure Setup Tool for Mattermost Embedded Plugin

This CLI tool automates the Azure AD application registration and configuration process required for the **Mattermost Mission Collaboration for Microsoft** plugin.

## Features

✅ **Automated Azure App Registration**
- Creates new Azure AD applications or updates existing ones
- Configures single-tenant authentication

✅ **API Permissions Configuration**
- `User.Read` (Delegated) - User authentication
- `TeamsActivity.Send` (Application) - Send notifications to Teams
- `AppCatalog.Read.All` (Application) - App catalog operations

✅ **API Exposure Setup**
- Application ID URI: `api://{hostname}/{client-id}`
- `access_as_user` scope for SSO
- Pre-authorized Microsoft clients (Teams Web/Desktop, Outlook Web/Desktop)

✅ **Client Secret Generation**
- Secure secret generation with configurable expiration (1-24 months)
- One-time display with security warnings

✅ **Multiple Authentication Methods**
- Environment variables (Service Principal)
- Azure CLI
- Interactive browser
- Device code flow

✅ **Configuration Doctor**
- Audits an existing registration without changing anything
- Checks the app, exposed API, permissions, admin consent, secrets, and housekeeping
- Produces a human, JSON, or Markdown report and exits non-zero on failures

✅ **Safety Features**
- Dry-run mode to preview changes
- Rollback on errors
- Idempotent operations
- Comprehensive validation

## Prerequisites

- **Azure Account** with permissions to manage applications
- **Required Azure AD Roles** (one of):
  - Application Administrator
  - Cloud Application Administrator
  - Global Administrator
- **Go 1.26.2+** (for building from source)

## Installation

### Build from Source

```bash
cd /path/to/mattermost-plugin-ms-embedded
go build -o azure-setup ./cmd/azure-setup/
```

### Build via Makefile

```bash
make azure-setup
# Binaries available in bin/ for multiple platforms
```

### Install to PATH

```bash
go install ./cmd/azure-setup
```

### Running Tests

```bash
# Run all tests
go test ./cmd/azure-setup/

# Run tests with coverage
go test -cover ./cmd/azure-setup/

# See TESTING.md for detailed testing documentation
```

## Quick Start

### 1. Validate Your Azure Access

```bash
azure-setup validate --verbose
```

This checks that you can authenticate to Azure and have the necessary permissions.

### 2. Create Azure Application (Dry Run)

```bash
azure-setup create \
  --site-url https://mattermost.example.com \
  --app-name "Mattermost for Teams" \
  --dry-run \
  --verbose
```

### 3. Create Azure Application (Live)

```bash
azure-setup create \
  --site-url https://mattermost.example.com \
  --app-name "Mattermost for Teams" \
  --verbose
```

### 4. Audit an Existing Application

```bash
azure-setup doctor \
  --client-id "abc123-def456-..." \
  --site-url https://mattermost.example.com
```

### 5. View Output in Different Formats

```bash
# Human-readable (default)
azure-setup create --site-url https://mm.example.com --app-name "My App"

# JSON format (for scripting)
azure-setup create --site-url https://mm.example.com --app-name "My App" -o json

# Environment variables
azure-setup create --site-url https://mm.example.com --app-name "My App" -o env

# Mattermost config.json format
azure-setup create --site-url https://mm.example.com --app-name "My App" -o mattermost
```

## Commands

### `azure-setup create`

Create or update an Azure AD application with all required configuration.

**Flags:**

| Flag | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `--site-url` | string | ✅ Yes | - | Mattermost site URL (must be HTTPS) |
| `--app-name` | string | No | "Mattermost for Teams" | Application display name |
| `--tenant-id` | string | No | - | Azure AD Tenant ID (auto-detected if omitted) |
| `--client-id` | string | No | - | Existing app client ID (for updates) |
| `--secret-expiration` | int | No | 12 | Secret expiration in months (1-24) |
| `--create-doctor-requirements` | bool | No | false | Also request the read-only Graph permissions `doctor` needs |
| `--dry-run` | bool | No | false | Preview changes without applying |
| `--verbose` / `-v` | bool | No | false | Enable verbose output |
| `--output` / `-o` | string | No | "human" | Output format: human, json, env, mattermost |
| `--non-interactive` | bool | No | false | Run without prompts |

**Examples:**

```bash
# Basic usage
azure-setup create --site-url https://mattermost.example.com

# Update existing app
azure-setup create \
  --site-url https://mattermost.example.com \
  --client-id "abc123-def456-..." \
  --verbose

# Custom secret expiration
azure-setup create \
  --site-url https://mattermost.example.com \
  --secret-expiration 24

# Output as environment variables
azure-setup create \
  --site-url https://mattermost.example.com \
  --output env > .env
```

### `azure-setup validate`

Validate Azure credentials and permissions without making changes.

**Flags:**

| Flag | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `--tenant-id` | string | No | - | Azure AD Tenant ID |
| `--verbose` / `-v` | bool | No | false | Enable verbose output |

**Example:**

```bash
azure-setup validate --verbose
```

### Permissions for the doctor

`azure-setup doctor` reads more of the directory than the plugin itself does, so
it needs two read-only Graph **application** permissions that the plugin never
uses: `Application.Read.All` and `Directory.Read.All`.

`--create-doctor-requirements` requests them on the same registration:

```bash
azure-setup create \
  --site-url https://mattermost.example.com \
  --create-doctor-requirements
```

The permission IDs are resolved from the Microsoft Graph service principal at
run time rather than hardcoded, so an unrecognised permission name fails with a
clear error instead of a rejected request.

**Security note.** Azure scopes permissions per *application*, not per
credential: every secret on this registration requests
`https://graph.microsoft.com/.default` and receives a token carrying every
consented app role. Once you grant consent, the client secret the plugin uses
also has tenant-wide directory read. If you need the plugin credential to stay
minimal, create a separate app registration for the doctor instead and leave
this flag off.

Admin consent still has to be granted by hand — it requires Privileged Role
Administrator or Global Administrator, which Application Administrator does not
cover. The consent link printed by `create` covers the added permissions too.

Re-running `create` **without** the flag does not remove the permissions: the
tool carries over any permission already on the application that it does not
manage, so rotating the plugin secret will not break the doctor.

### `azure-setup doctor`

Audit an existing Azure AD application and report whether every setting the plugin
depends on is configured correctly. The doctor is read-only: it never changes Azure.

**Checks performed:**

| Category | Checks |
|----------|--------|
| Identity & access | Sign-in succeeds, the signed-in user holds an application administration role, tenant is resolved |
| Application registration | The application exists, is single tenant, and its Application ID URI matches the Mattermost site URL |
| Exposed API | The `access_as_user` scope is exposed, enabled, user-consentable, and fully described; every Microsoft first-party client (Teams, Outlook, Office, Copilot) is pre-authorized for it |
| API permissions | `User.Read`, `TeamsActivity.Send`, and `AppCatalog.Read.All` are requested with the correct type. Any other permission — including the doctor's own `Application.Read.All` / `Directory.Read.All` — is listed by name but never affects the status |
| Admin consent | A service principal exists and is enabled, and each delegated and application permission has been consented tenant-wide |
| Credentials | At least one client secret is valid, with a warning before it expires; expired secrets and certificates are reported |
| Housekeeping | The application has owners, and no other registration shares its display name |

**Flags:**

| Flag | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `--client-id` | string | No | - | Client ID of the application to inspect (preferred over `--app-name`) |
| `--app-name` | string | No | "Mattermost for Teams" | Display name to look up when `--client-id` is not given |
| `--site-url` | string | No | - | Mattermost site URL, used to verify the Application ID URI |
| `--tenant-id` | string | No | - | Azure AD Tenant ID (auto-detected if omitted) |
| `--output` / `-o` | string | No | "human" | Report format: human, json, markdown |
| `--report-file` | string | No | - | Also write the report to this file |
| `--secret-warning-days` | int | No | 30 | Warn when a client secret expires within this many days |
| `--cloud` | string | No | "commercial" | Microsoft national cloud: commercial, gcchigh, dod |
| `--verbose` / `-v` | bool | No | false | Enable verbose output |

**Exit code:** `0` when every check passes or only warnings are raised, non-zero when
any check fails. This makes the doctor usable as a health check in CI or a cron job.

A check that could not run — because the credential cannot read the consent
grants, for example — is reported as `SKIP` and downgrades the overall verdict to
`warn`, never `pass`, so an incomplete report cannot be mistaken for a clean one.
Automation that needs certainty should assert on `summary.status == "pass"` in the
JSON output rather than on the exit code alone.

**Examples:**

```bash
# Full audit of a known application
azure-setup doctor \
  --client-id "abc123-def456-..." \
  --site-url https://mattermost.example.com

# Save a Markdown report to attach to a support ticket
azure-setup doctor \
  --client-id "abc123-def456-..." \
  --site-url https://mattermost.example.com \
  --output markdown \
  --report-file azure-report.md

# Machine-readable output for monitoring
azure-setup doctor --client-id "abc123-def456-..." -o json
```

**Sample report:**

```
======================================================================
🩺 AZURE SETUP DOCTOR REPORT
======================================================================
Generated:             2026-01-15 12:00:00 UTC
National cloud:        commercial
Tenant ID:             8f2b1a90-1111-2222-3333-444455556666
Signed in as:          admin@contoso.onmicrosoft.com
Application:           Mattermost for Teams
Client ID:             11111111-2222-3333-4444-555555555555
Application ID URI:    api://mattermost.example.com/11111111-...

Exposed API
----------------------------------------------------------------------
✅ Exposed scope (access_as_user)
     Exposed, enabled, and consentable by users
❌ Pre-authorized Microsoft clients
     2 of 9 Microsoft clients are not pre-authorized for access_as_user,
     so SSO will prompt for consent there
       - missing: 5e3ce6c0-... (Microsoft Teams web)
       - missing: 1fec8e78-... (Microsoft Teams desktop and mobile)
     ↳ Fix: Re-run `azure-setup create --client-id 11111111-...`

======================================================================
SUMMARY: 12 passed · 2 warning(s) · 2 failed · 0 skipped
======================================================================
❌ The Azure application is not fully configured - the plugin may not work.

📝 ACTION ITEMS
----------------------------------------------------------------------
1. [FAIL] Pre-authorized Microsoft clients: 2 of 9 Microsoft clients are
   not pre-authorized for access_as_user
   → Re-run `azure-setup create --client-id 11111111-...`
2. [WARN] Client secrets: Every valid secret expires within 30 day(s)
   → Rotate the client secret before it expires to avoid an outage
```

## Authentication Methods

The tool tries multiple authentication methods in order:

### 1. Environment Variables (Service Principal)

Set these environment variables:

```bash
export AZURE_TENANT_ID="your-tenant-id"
export AZURE_CLIENT_ID="your-client-id"
export AZURE_CLIENT_SECRET="your-client-secret"
```

### 2. Azure CLI

If you're logged in via Azure CLI:

```bash
az login
azure-setup create --site-url https://mattermost.example.com
```

### 3. Interactive Browser

Opens a browser window for interactive authentication.

### 4. Device Code Flow

For headless environments, provides a device code to enter on another device.

## Output Formats

### Human-Readable (default)

Provides clear, formatted output with next steps:

```
======================================================================
✅ AZURE SETUP COMPLETE
======================================================================

🔐 CREDENTIALS FOR MATTERMOST CONFIGURATION
----------------------------------------------------------------------
Tenant ID:             abc123...
Application Client ID: def456...
Client Secret:         secret-value
Secret Expires:        2026-01-15 12:00:00 UTC
----------------------------------------------------------------------

📝 NEXT STEPS
1. Grant admin consent for API permissions
2. Configure the Mattermost plugin
3. Download and install the Teams app manifest
```

### JSON Format

Machine-readable JSON for scripting:

```bash
azure-setup create --site-url https://mm.example.com -o json | jq .
```

### Environment Variables Format

Ready-to-use environment variable exports:

```bash
azure-setup create --site-url https://mm.example.com -o env >> .env
source .env
```

### Mattermost Config Format

Snippet to merge into Mattermost `config.json`:

```bash
azure-setup create --site-url https://mm.example.com -o mattermost > plugin-config.json
```

## Troubleshooting

Start with `azure-setup doctor --client-id <id> --site-url <url>`: it inspects the
live registration and prints the exact settings that are missing or wrong, together
with the command or portal link that fixes each one.

### Authentication Failures

**Problem:** "Failed to authenticate to Azure"

**Solutions:**
- Ensure you're logged into Azure CLI: `az login`
- Or set service principal environment variables
- Check network connectivity to Azure

### Permission Errors

**Problem:** "Failed to create application: insufficient privileges"

**Solutions:**
- Verify you have one of the required Azure AD roles
- Contact your Azure AD administrator to grant permissions
- Check the role assignments in Azure Portal → Azure AD → Roles and administrators

### Dry Run Shows Errors

**Problem:** Errors appear even in dry-run mode

**Solution:** Dry-run validation errors indicate issues with input parameters, not Azure operations. Fix the parameters and try again.

### SSO Prompts for Consent Inside Teams or Outlook

**Problem:** Users are asked to consent when opening the tab, or sign-in fails silently

**Solutions:**
- Run `azure-setup doctor` and look at the **Exposed API** and **Admin consent** sections
- A missing pre-authorized client means SSO will prompt in that specific Microsoft app
- Missing admin consent means the permission was requested but never granted

### The Plugin Stops Working After Months

**Problem:** Authentication worked and then began failing

**Solution:** The client secret has most likely expired. Run `azure-setup doctor` to see
every secret and its expiry date, then rotate it with
`azure-setup create --client-id <id> --site-url <url>`.

## Security Best Practices

### Client Secret Management

🔒 **IMPORTANT:**
- Client secrets are shown **only once** - save them immediately
- Store secrets securely (e.g., Azure Key Vault, HashiCorp Vault)
- Never commit secrets to version control
- Rotate secrets before expiration (set calendar reminders)

### Principle of Least Privilege

- Only grant the minimum required API permissions
- Use service principals for automation (not user accounts)
- Regularly audit application permissions

### Secret Rotation

Set up secret rotation before expiration:

```bash
# Generate new secret with 12 month expiration
azure-setup create \
  --site-url https://mattermost.example.com \
  --client-id "existing-client-id" \
  --secret-expiration 12
```

## Integration with CI/CD

### GitHub Actions Example

```yaml
name: Setup Azure AD App

on:
  workflow_dispatch:
    inputs:
      site_url:
        description: 'Mattermost Site URL'
        required: true

jobs:
  setup:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.26.2'

      - name: Build Azure Setup Tool
        run: go build -o azure-setup ./cmd/azure-setup/

      - name: Configure Azure Application
        env:
          AZURE_TENANT_ID: ${{ secrets.AZURE_TENANT_ID }}
          AZURE_CLIENT_ID: ${{ secrets.AZURE_CLIENT_ID }}
          AZURE_CLIENT_SECRET: ${{ secrets.AZURE_CLIENT_SECRET }}
        run: |
          ./azure-setup create \
            --site-url ${{ github.event.inputs.site_url }} \
            --output json \
            --non-interactive \
            > azure-config.json

      - name: Upload Configuration
        uses: actions/upload-artifact@v3
        with:
          name: azure-configuration
          path: azure-config.json
```

## Next Steps After Running the Tool

### 1. Grant Admin Consent

Visit the Azure Portal URL provided in the output to grant admin consent for API permissions.

### 2. Configure Mattermost Plugin

In Mattermost:
1. Go to **System Console > Plugins > Mattermost Embedded**
2. Enter the credentials from the tool output:
   - Tenant ID
   - Application Client ID
   - Client Secret
3. Save the configuration

### 3. Generate and Upload Teams App Manifest

In the Mattermost plugin settings:
1. Download the Teams app manifest
2. Go to **Microsoft Teams Admin Center**
3. Navigate to **Teams apps > Manage apps > Upload**
4. Upload the manifest ZIP file

### 4. Test the Integration

1. Install the app in Microsoft Teams
2. Add the Mattermost tab to a team
3. Verify SSO authentication works
4. Test notifications from Mattermost to Teams

## Support and Contributing

For issues, questions, or contributions:
- **GitHub Issues**: https://github.com/mattermost/mattermost-plugin-ms-embedded/issues
- **Documentation**: https://docs.mattermost.com/integrations-guide/mattermost-mission-collaboration-for-m365.html
- **Community**: https://community.mattermost.com/

## License

Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
