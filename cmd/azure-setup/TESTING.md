# Azure Setup CLI Tool - Testing Documentation

## Test Overview

Comprehensive unit and orchestration tests have been implemented for the Azure Setup CLI tool to ensure reliability and correctness.

## Test Statistics

- **Total Test Files**: 6
- **Total Lines of Test Code**: ~1,250 lines
- **Test Coverage**: 24.6% of statements
- **All Tests Passing**: ✅ Yes
- **Orchestration Tests**: Complete workflow validation

## Test Types

This project includes multiple types of tests:

1. **Unit Tests** - Test individual functions and components in isolation
   - Run by default with `go test`
   - Fast execution (~5ms)
   - No external dependencies

2. **Orchestration Tests** - Test complete workflows using mocks
   - Run by default with `go test`
   - Test business logic and orchestration
   - Use mock Azure SDK objects
   - **No Azure credentials required** ✅
   - Safe to run in CI/CD environments

## Unit Test Files

### 1. `validate_test.go` - Validation Logic Tests

Tests for input validation and helper functions:

**Test Cases:**
- ✅ `TestValidateInputs` - 9 test scenarios
- ✅ `TestEscapeODataString` - 7 test scenarios (OData filter escaping)
  - Valid configuration
  - Missing site URL
  - Invalid site URL (no protocol, not HTTPS)
  - Missing app name / app name too short
  - Secret expiration validation
  - Default value handling
  - Site URL with path support

- ✅ `TestExtractHostnameAndPath` - 7 test scenarios
  - Simple domain extraction
  - Domain with port
  - Domain with path (single and nested)
  - Trailing slash handling
  - Invalid URL error handling

- ✅ `TestBuildApplicationIDURI` - 7 test scenarios
  - Various URL formats
  - Port handling
  - Path handling
  - Client ID integration
  - Error cases

- ✅ `TestIsApplicationAdminRole` - 5 test scenarios
  - Application Administrator role
  - Cloud Application Administrator role
  - Global Administrator role
  - Unknown/non-admin roles

**Total Tests**: 35 test cases

### 2. `output_test.go` - Output Formatting Tests

Tests for all output formats:

**Test Cases:**
- ✅ `TestOutputJSON` - JSON output format
  - Valid JSON structure
  - All fields present
  - Proper serialization

- ✅ `TestOutputEnv` - Environment variables format
  - Export statements generated
  - M365 variables present
  - Mattermost plugin variables present
  - Failure handling

- ✅ `TestOutputMattermostConfig` - Mattermost config.json format
  - Valid JSON structure
  - Proper plugin configuration format
  - Failure handling

- ✅ `TestOutputHuman` - Human-readable format
  - Successful creation output
  - Successful update output
  - Dry run mode output
  - Failure output

- ✅ `TestOutputResult` - Output format dispatcher
  - All format types (human, json, env, mattermost)
  - Default format handling
  - Invalid format error handling

**Total Tests**: 17 test cases

### 3. `types_test.go` - Data Structures and Constants Tests

Tests for type definitions and helper functions:

**Test Cases:**
- ✅ `TestGetRequiredPermissions` - Permission list validation
  - 3 permissions present
  - Correct IDs and types
  - User.Read (Delegated)
  - TeamsActivity.Send (Application)
  - AppCatalog.Read.All (Application)

- ✅ `TestGetPreAuthorizedClients` - Pre-authorized clients validation
  - 4 clients present
  - Teams Web/Desktop
  - Outlook Web/Desktop

- ✅ `TestPermissionConstants` - Permission ID validation
  - Graph Resource ID
  - Permission UUIDs format
  - Permission types

- ✅ `TestPreAuthorizedClientConstants` - Client ID validation
  - UUID format validation
  - Known client IDs verification

- ✅ `TestScopeConstants` - Scope configuration
  - Scope name
  - Scope description
  - User consent level

- ✅ `TestSetupConfigDefaults` - Configuration structure
  - Default values
  - Rollback slice initialization

- ✅ `TestSetupResultStructure` - Result structure
  - All fields accessible
  - Proper data types

**Total Tests**: 7 test suites with multiple assertions

### 4. `rollback_test.go` - Rollback Mechanism Tests

Tests for error recovery and cleanup:

**Test Cases:**
- ✅ `TestExecuteRollback` - Rollback execution
  - Empty rollback list handling
  - Single rollback function
  - Multiple functions in reverse order
  - Continues on rollback error
  - All functions fail handling
  - Verbose mode

- ✅ `TestRollbackIntegration` - Integration testing
  - Resource tracking
  - Cleanup verification

**Total Tests**: 8 test scenarios

### 5. `mocks_test.go` - Mock Helper Functions

Helper functions to create mock Azure SDK objects for testing:

**Mock Functions:**
- `newMockApplication` - Create mock Azure AD application
- `newMockServicePrincipal` - Create mock service principal
- `newMockPasswordCredential` - Create mock client secret
- `newMockApplicationCollection` - Create mock application collection
- `newMockServicePrincipalCollection` - Create mock service principal collection
- `newMockOrganization` - Create mock organization
- `newMockUser` - Create mock user
- `newMockDirectoryRole` - Create mock directory role

### 6. `orchestration_test.go` - Orchestration Tests

Tests for complete workflows using mocks (no Azure credentials required):

**Test Cases:**
- ✅ `TestOrchestrationCreateNewApp` - Complete workflow validation
- ✅ `TestOrchestrationDryRun` - Dry-run mode behavior
- ✅ `TestOrchestrationRollback` - Rollback execution order
- ✅ `TestOrchestrationInputValidation` - Input validation scenarios
- ✅ `TestOrchestrationApplicationIDURI` - Application ID URI generation
- ✅ `TestOrchestrationOutputFormats` - All output format types
- ✅ `TestOrchestrationPermissionConstants` - Permission constant validation
- ✅ `TestOrchestrationPreAuthorizedClients` - Pre-authorized client validation
- ✅ `TestOrchestrationBuildRequiredResourceAccess` - Permission structure building
- ✅ `TestOrchestrationBuildPreAuthorizedApplications` - Pre-authorized app building
- ✅ `TestOrchestrationErrorScenarios` - Error handling
- ✅ `TestOrchestrationContextHandling` - Context timeout behavior

**Total Tests**: 12 orchestration scenarios

**Benefits:**
- ✅ No Azure credentials required
- ✅ Safe to run in CI/CD
- ✅ Fast execution
- ✅ Tests business logic and orchestration
- ✅ Validates all workflow paths

### 7. `doctor_test.go` - Configuration Doctor Tests

Tests for the read-only `doctor` command. Every check is a pure function over a
`doctorInputs` fixture, so the full rule set is exercised without a live tenant.

**Test Cases:**
- ✅ `TestRunDoctorChecksHealthyApplication` - A correctly configured app passes every check
- ✅ `TestCheckSignInAudience` - Single tenant, multi tenant, and unset audiences
- ✅ `TestCheckApplicationIDURI` - Matching, missing, mismatched, and ambiguous identifier URIs
- ✅ `TestCheckExposedScope` - Missing, disabled, admin-only, and undocumented `access_as_user` scope
- ✅ `TestCheckPreAuthorizedClients` - Missing clients, stale scope IDs, and unmanaged extras
- ✅ `TestCheckRequiredPermissions` - Missing permissions and wrong permission types
- ✅ `TestCheckServicePrincipal` - Enabled, disabled, missing, and unreadable service principals
- ✅ `TestCheckDelegatedConsent` / `TestCheckAppRoleConsent` - Tenant-wide, per-user, and missing consent
- ✅ `TestCheckClientSecrets` - Valid, expiring, and fully expired secrets
- ✅ `TestCheckCertificates`, `TestCheckOwners`, `TestCheckDuplicateApplications` - Housekeeping rules
- ✅ `TestDoctorReportFinalize` - Overall status precedence (fail > warn > pass); a `StatusSkip` alongside a `StatusPass` downgrades the overall status to `warn` and is included in the counts, so an unverified check never reports as healthy
- ✅ `TestRenderDoctorReportHuman` / `JSON` / `Markdown` - All three report formats

**Benefits:**
- ✅ No Azure credentials required
- ✅ Each rule is asserted independently, so a regression names the broken check

## Running Tests

### Run All Unit Tests

```bash
go test ./cmd/azure-setup/
```

### Run Tests with Verbose Output

```bash
go test -v ./cmd/azure-setup/
```

### Run Tests with Coverage

```bash
go test -cover ./cmd/azure-setup/
```

### Generate Coverage Report

```bash
go test -coverprofile=coverage.out ./cmd/azure-setup/
go tool cover -html=coverage.out
```

### Run Specific Test

```bash
go test -v ./cmd/azure-setup/ -run TestValidateInputs
```

### Run Specific Test Case

```bash
go test -v ./cmd/azure-setup/ -run TestValidateInputs/valid_configuration
```

### Run Orchestration Tests

```bash
# Run all orchestration tests (no Azure credentials needed)
go test -v ./cmd/azure-setup/ -run TestOrchestration

# Run specific orchestration test
go test -v ./cmd/azure-setup/ -run TestOrchestrationCreateNewApp
```

## Test Coverage Details

### Well-Covered Modules

✅ **Validation Logic** (`validate.go`)
- Input validation: 100% covered
- URL parsing: 100% covered
- Helper functions: 100% covered
- OData escaping: 100% covered

✅ **Output Formatting** (`output.go`)
- All format types tested
- Error handling covered
- Output structure validation

✅ **Type Definitions** (`types.go`)
- All constants validated
- Permission lists verified
- Client IDs checked

✅ **Rollback Logic** (`rollback.go`)
- Execution order verified
- Error handling tested
- Integration scenarios covered

✅ **Orchestration Logic** (`orchestration_test.go`)
- Complete workflow paths tested
- Error scenarios covered
- Business logic validated
- All without requiring Azure credentials

### Modules Not Covered by Automated Tests

⚠️ **Azure SDK Integration** (`auth.go`, `app.go`, `permissions.go`, `expose.go`, `secret.go`)
- These modules make actual calls to Azure SDK
- Business logic and orchestration is tested via orchestration tests
- Azure SDK calls are best tested through manual validation
- Orchestration tests validate the logic without hitting real Azure APIs

**Why not mock Azure SDK directly?**
- Microsoft Graph SDK has complex internal types that are difficult to mock
- Orchestration tests validate business logic without requiring Azure SDK mocks
- Manual testing with real Azure tenant remains the best validation for SDK integration
- Mocking SDK would create brittle tests tied to SDK implementation details

## Testing Best Practices Followed

### ✅ Table-Driven Tests
All test functions use table-driven test patterns for clarity and maintainability.

```go
tests := []struct {
    name        string
    config      *SetupConfig
    expectError bool
    errorMsg    string
}{
    // test cases...
}
```

### ✅ Descriptive Test Names
Test names clearly describe what is being tested:
- `TestValidateInputs/valid_configuration`
- `TestOutputJSON`
- `TestExecuteRollback/multiple_rollback_functions_in_reverse_order`

### ✅ Assertions with Context
All assertions include helpful context messages:

```go
assert.Equal(t, expected, actual, "should contain client ID: %s", expected)
```

### ✅ Error Testing
Both success and failure cases are tested:
- Valid inputs
- Invalid inputs
- Edge cases
- Error messages

### ✅ Integration Tests
Rollback tests include integration scenarios that test multiple components together.

## Test Maintenance

### Adding New Tests

1. Create test file named `<module>_test.go`
2. Use table-driven test pattern
3. Include both positive and negative test cases
4. Add descriptive test names
5. Run tests to verify: `go test ./cmd/azure-setup/`

### Updating Tests

When modifying code:
1. Update corresponding tests
2. Add tests for new functionality
3. Verify all tests pass
4. Check coverage hasn't decreased

## Testing Philosophy

### Why Orchestration Tests Instead of Full Azure Mocks?

We use orchestration tests that validate business logic without mocking the Azure SDK for several reasons:

1. **Azure SDK Complexity**: Microsoft Graph SDK uses complex internal types that are difficult to mock accurately
2. **Test Maintainability**: Mocking SDK internals creates brittle tests tied to implementation details
3. **Business Logic Focus**: Orchestration tests validate the important logic (input validation, URI building, permission configuration)
4. **CI/CD Friendly**: No Azure credentials needed, fast execution
5. **Real Validation**: Manual testing with real Azure tenant provides better validation for SDK integration

### What Gets Tested

✅ **Covered by Automated Tests:**
- Input validation logic
- URL parsing and Application ID URI construction
- Permission structure building
- Pre-authorized client configuration
- Rollback mechanism
- Output formatting
- Error handling
- Complete workflow orchestration

⚠️ **Requires Manual Testing:**
- Azure authentication (various methods)
- Actual Azure AD application creation/modification
- Microsoft Graph API calls
- Admin consent workflow
- Real secret generation

### Manual Testing

For manual validation with a real Azure tenant:

```bash
# Authenticate with Azure CLI
az login

# Run the tool with verbose output
./azure-setup create \
  --site-url https://your-mattermost-site.com \
  --app-name "Test Application" \
  --verbose \
  --dry-run

# After validating dry-run, run for real
./azure-setup create \
  --site-url https://your-mattermost-site.com \
  --app-name "Test Application" \
  --verbose
```

## CI/CD Integration

### GitHub Actions Example

```yaml
- name: Run All Tests
  run: go test -v ./cmd/azure-setup/

- name: Check Coverage
  run: |
    go test -coverprofile=coverage.out ./cmd/azure-setup/
    go tool cover -func=coverage.out

- name: Run Orchestration Tests Specifically
  run: go test -v ./cmd/azure-setup/ -run TestOrchestration
```

### Benefits for CI/CD

✅ **No Azure Credentials Required**
- Tests run without any Azure authentication
- No secrets management needed
- Safe for public CI/CD pipelines

✅ **Fast Execution**
- All tests complete in < 1 second
- No network calls to Azure
- Parallel execution friendly

✅ **Reliable**
- No dependency on Azure service availability
- No rate limiting concerns
- Deterministic results

## Manual Testing Checklist

For Azure SDK integration, perform manual testing with a real Azure tenant:

### Prerequisites
- [ ] Access to Azure AD tenant
- [ ] Application Administrator role or higher
- [ ] Azure CLI installed (or service principal credentials)

### Authentication Testing
- [ ] Test with Azure CLI authentication (`az login`)
- [ ] Test with service principal (environment variables)
- [ ] Test with interactive browser (if available)
- [ ] Verify permission validation works

### Functionality Testing
- [ ] Test dry-run mode (no resources created)
- [ ] Test creating new application
- [ ] Test updating existing application
- [ ] Test permission configuration (verify in Azure Portal)
- [ ] Test API exposure setup (verify Application ID URI)
- [ ] Test secret generation and expiration
- [ ] Test rollback on error (verify cleanup)
- [ ] Test all output formats (human, json, env, mattermost)

### Edge Cases
- [ ] Test with various Mattermost URLs (with/without paths, ports)
- [ ] Test with app name containing special characters
- [ ] Test with different secret expiration values (1-24 months)
- [ ] Test creating app with same name twice
- [ ] Test updating app by client ID vs. app name

### Verification in Azure Portal
- [ ] Application appears in App Registrations
- [ ] Correct permissions configured (User.Read, TeamsActivity.Send, AppCatalog.Read.All)
- [ ] Application ID URI matches expected format
- [ ] Pre-authorized clients configured (Teams, Outlook)
- [ ] OAuth2 scope (access_as_user) present
- [ ] Service principal created

## Summary

✅ **Comprehensive test coverage** for core validation, orchestration, and output logic
✅ **All tests passing** with no failures
✅ **No Azure credentials required** for automated tests
✅ **Safe for CI/CD** - all tests run without external dependencies
✅ **Table-driven tests** for maintainability
✅ **~1,250 lines of test code** ensuring quality
✅ **12 orchestration test scenarios** validating complete workflows
✅ **Fast execution** - all tests complete in milliseconds

The test suite validates:
- Input validation and sanitization
- URL parsing and Application ID URI construction
- Permission and client configuration
- Rollback mechanism
- Error handling
- Complete workflow orchestration
- All output formats

This provides confidence in the tool's reliability while maintaining practical test coverage that can run anywhere without Azure access.
