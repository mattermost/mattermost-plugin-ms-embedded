// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pkg/errors"
)

// CheckStatus is the outcome of a single doctor check.
type CheckStatus string

const (
	// StatusPass means the setting matches what the plugin needs.
	StatusPass CheckStatus = "pass"
	// StatusWarn means the setting works today but needs attention (for example a secret about to expire).
	StatusWarn CheckStatus = "warn"
	// StatusFail means the setting is missing or wrong and the plugin will not work correctly.
	StatusFail CheckStatus = "fail"
	// StatusSkip means the check could not run (for example the caller lacks permission to read the data).
	StatusSkip CheckStatus = "skip"
)

// Icon returns the emoji used to render the status in the human report.
func (s CheckStatus) Icon() string {
	switch s {
	case StatusPass:
		return "✅"
	case StatusWarn:
		return "⚠️ "
	case StatusFail:
		return "❌"
	case StatusSkip:
		return "⏭️ "
	default:
		return "•"
	}
}

// Label returns the uppercase status label used in the markdown report.
func (s CheckStatus) Label() string {
	switch s {
	case StatusPass:
		return "PASS"
	case StatusWarn:
		return "WARN"
	case StatusFail:
		return "FAIL"
	case StatusSkip:
		return "SKIP"
	default:
		return "UNKNOWN"
	}
}

// Doctor check categories, listed in the order they are rendered.
const (
	CategoryIdentity     = "Identity & access"
	CategoryApplication  = "Application registration"
	CategoryExposedAPI   = "Exposed API"
	CategoryPermissions  = "API permissions"
	CategoryConsent      = "Admin consent"
	CategoryCredentials  = "Credentials"
	CategoryHousekeeping = "Housekeeping"
)

// CheckResult is a single finding in the doctor report.
type CheckResult struct {
	Category    string      `json:"category"`
	Name        string      `json:"name"`
	Status      CheckStatus `json:"status"`
	Summary     string      `json:"summary"`
	Details     []string    `json:"details,omitempty"`
	Remediation string      `json:"remediation,omitempty"`
}

// DoctorReport is the full result of a doctor run.
type DoctorReport struct {
	GeneratedAt string `json:"generated_at"`
	ToolVersion string `json:"tool_version"`
	Cloud       string `json:"cloud"`

	TenantID   string `json:"tenant_id,omitempty"`
	SignedInAs string `json:"signed_in_as,omitempty"`

	ApplicationName     string `json:"application_name,omitempty"`
	ApplicationClientID string `json:"application_client_id,omitempty"`
	ApplicationObjectID string `json:"application_object_id,omitempty"`
	ApplicationIDURI    string `json:"application_id_uri,omitempty"`
	MattermostSiteURL   string `json:"mattermost_site_url,omitempty"`
	PortalHost          string `json:"portal_host,omitempty"`

	Checks  []CheckResult `json:"checks"`
	Summary ReportSummary `json:"summary"`
}

// ReportSummary holds the per-status check counts plus the overall verdict.
type ReportSummary struct {
	Passed  int         `json:"passed"`
	Warned  int         `json:"warned"`
	Failed  int         `json:"failed"`
	Skipped int         `json:"skipped"`
	Status  CheckStatus `json:"status"`
}

// Add appends a check result to the report.
func (r *DoctorReport) Add(check CheckResult) {
	r.Checks = append(r.Checks, check)
}

// Finalize recomputes the summary counters and the overall status. It must be
// called once every check has been added and before the report is rendered.
func (r *DoctorReport) Finalize() {
	summary := ReportSummary{Status: StatusPass}

	for _, check := range r.Checks {
		switch check.Status {
		case StatusPass:
			summary.Passed++
		case StatusWarn:
			summary.Warned++
		case StatusFail:
			summary.Failed++
		case StatusSkip:
			summary.Skipped++
		}
	}

	// A skipped check is an unverified check. Leaving the verdict at "pass"
	// would let an incomplete run - a credential that cannot read the consent
	// grants, say - report a clean bill of health and exit 0 in CI.
	switch {
	case summary.Failed > 0:
		summary.Status = StatusFail
	case summary.Warned > 0, summary.Skipped > 0:
		summary.Status = StatusWarn
	}

	r.Summary = summary
}

// categories returns the distinct check categories in first-seen order.
func (r *DoctorReport) categories() []string {
	var ordered []string
	seen := make(map[string]bool, len(r.Checks))

	for _, check := range r.Checks {
		if !seen[check.Category] {
			seen[check.Category] = true
			ordered = append(ordered, check.Category)
		}
	}

	return ordered
}

// actionItems returns every failing or warning check, failures first, so the
// report can close with a prioritized to-do list.
func (r *DoctorReport) actionItems() []CheckResult {
	var items []CheckResult

	for _, status := range []CheckStatus{StatusFail, StatusWarn} {
		for _, check := range r.Checks {
			if check.Status == status {
				items = append(items, check)
			}
		}
	}

	return items
}

// normalizeReportFormat validates a doctor report format and returns its
// canonical name. It is called before the doctor touches Azure so an unusable
// format fails immediately rather than after a login and a full inspection.
func normalizeReportFormat(format string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(format)); normalized {
	case "human", "":
		return "human", nil
	case "json":
		return "json", nil
	case "markdown", "md":
		return "markdown", nil
	default:
		return "", errors.Errorf("unknown report format: %s (must be one of human, json, markdown)", format)
	}
}

// RenderDoctorReport writes the report to w in the requested format.
func RenderDoctorReport(w io.Writer, report *DoctorReport, format string) error {
	normalized, err := normalizeReportFormat(format)
	if err != nil {
		return err
	}

	var rendered string

	switch normalized {
	case "json":
		rendered, err = renderDoctorJSON(report)
	case "markdown":
		rendered = renderDoctorMarkdown(report)
	default:
		rendered = renderDoctorHuman(report)
	}
	if err != nil {
		return err
	}

	if _, err = io.WriteString(w, rendered); err != nil {
		return errors.Wrap(err, "failed to write report")
	}

	return nil
}

func renderDoctorJSON(report *DoctorReport) (string, error) {
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", errors.Wrap(err, "failed to encode report as JSON")
	}

	return string(jsonBytes) + "\n", nil
}

func renderDoctorHuman(report *DoctorReport) string {
	var b strings.Builder
	rule := strings.Repeat("=", 70)

	fmt.Fprintf(&b, "\n%s\n", rule)
	fmt.Fprintf(&b, "🩺 AZURE SETUP DOCTOR REPORT\n")
	fmt.Fprintf(&b, "%s\n", rule)

	for _, field := range reportHeaderFields(report) {
		fmt.Fprintf(&b, "%-22s %s\n", field[0]+":", field[1])
	}

	for _, category := range report.categories() {
		fmt.Fprintf(&b, "\n%s\n%s\n", category, strings.Repeat("-", 70))

		for _, check := range report.Checks {
			if check.Category != category {
				continue
			}

			fmt.Fprintf(&b, "%s %s\n", check.Status.Icon(), check.Name)
			fmt.Fprintf(&b, "     %s\n", check.Summary)

			for _, detail := range check.Details {
				fmt.Fprintf(&b, "       - %s\n", detail)
			}

			if check.Remediation != "" && check.Status != StatusPass {
				fmt.Fprintf(&b, "     ↳ Fix: %s\n", check.Remediation)
			}
		}
	}

	fmt.Fprintf(&b, "\n%s\n", rule)
	fmt.Fprintf(&b, "SUMMARY: %d passed · %d warning(s) · %d failed · %d skipped\n",
		report.Summary.Passed, report.Summary.Warned, report.Summary.Failed, report.Summary.Skipped)
	fmt.Fprintf(&b, "%s\n", rule)

	switch report.Summary.Status {
	case StatusPass:
		fmt.Fprintf(&b, "✅ Everything checks out - the Azure application is configured correctly.\n")
	case StatusWarn:
		fmt.Fprintf(&b, "⚠️  The plugin should work, but some items need attention.\n")
	case StatusFail:
		fmt.Fprintf(&b, "❌ The Azure application is not fully configured - the plugin may not work.\n")
	}

	if report.Summary.Skipped > 0 {
		fmt.Fprintf(&b, "⚠️  %d check(s) could not run, so this report is incomplete.\n", report.Summary.Skipped)
	}

	if items := report.actionItems(); len(items) > 0 {
		fmt.Fprintf(&b, "\n📝 ACTION ITEMS\n%s\n", strings.Repeat("-", 70))
		for i, check := range items {
			fmt.Fprintf(&b, "%d. [%s] %s: %s\n", i+1, check.Status.Label(), check.Name, check.Summary)
			if check.Remediation != "" {
				fmt.Fprintf(&b, "   → %s\n", check.Remediation)
			}
		}
	}

	return b.String()
}

func renderDoctorMarkdown(report *DoctorReport) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Azure Setup Doctor Report\n\n")

	for _, field := range reportHeaderFields(report) {
		fmt.Fprintf(&b, "- **%s:** %s\n", field[0], field[1])
	}

	fmt.Fprintf(&b, "\n**Result:** %s — %d passed, %d warning(s), %d failed, %d skipped\n",
		report.Summary.Status.Label(), report.Summary.Passed, report.Summary.Warned,
		report.Summary.Failed, report.Summary.Skipped)

	for _, category := range report.categories() {
		fmt.Fprintf(&b, "\n## %s\n\n", category)
		fmt.Fprintf(&b, "| Status | Check | Result |\n|---|---|---|\n")

		for _, check := range report.Checks {
			if check.Category != category {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n",
				check.Status.Label(), escapeMarkdownCell(check.Name), escapeMarkdownCell(check.Summary))
		}

		for _, check := range report.Checks {
			if check.Category != category || len(check.Details) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n<details><summary>%s — details</summary>\n\n", escapeMarkdownCell(check.Name))
			for _, detail := range check.Details {
				fmt.Fprintf(&b, "- %s\n", detail)
			}
			fmt.Fprintf(&b, "\n</details>\n")
		}
	}

	if items := report.actionItems(); len(items) > 0 {
		fmt.Fprintf(&b, "\n## Action items\n\n")
		for _, check := range items {
			fmt.Fprintf(&b, "1. **[%s] %s** — %s\n", check.Status.Label(), check.Name, check.Summary)
			if check.Remediation != "" {
				fmt.Fprintf(&b, "   - Fix: %s\n", check.Remediation)
			}
		}
	}

	return b.String()
}

// reportHeaderFields returns the label/value pairs shown at the top of every
// report format, skipping fields that were not resolved during the run.
func reportHeaderFields(report *DoctorReport) [][2]string {
	candidates := [][2]string{
		{"Generated", report.GeneratedAt},
		{"Tool version", report.ToolVersion},
		{"National cloud", report.Cloud},
		{"Tenant ID", report.TenantID},
		{"Signed in as", report.SignedInAs},
		{"Application", report.ApplicationName},
		{"Client ID", report.ApplicationClientID},
		{"Object ID", report.ApplicationObjectID},
		{"Application ID URI", report.ApplicationIDURI},
		{"Mattermost site URL", report.MattermostSiteURL},
	}

	var fields [][2]string
	for _, candidate := range candidates {
		if candidate[1] != "" {
			fields = append(fields, candidate)
		}
	}

	return fields
}

// escapeMarkdownCell keeps a value from breaking the table layout. Pipes are
// escaped, and all whitespace is collapsed because a newline terminates the row
// outright - Graph OData errors, which end up in skip summaries, carry them.
func escapeMarkdownCell(value string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(value), " "), "|", "\\|")
}
