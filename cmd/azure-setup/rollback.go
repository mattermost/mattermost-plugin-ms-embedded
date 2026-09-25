// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"slices"
)

// executeRollback executes all rollback functions in reverse order
func executeRollback(config *SetupConfig) {
	if len(config.rollback) == 0 {
		return
	}

	progressln("\n🔄 Executing rollback operations...")

	// Execute rollback functions in reverse order
	// Track failures so we can warn about potentially leaked resources
	var failures []error
	for _, rollbackFunc := range slices.Backward(config.rollback) {
		if err := rollbackFunc(); err != nil {
			failures = append(failures, err)
			progressf("   ⚠️  Rollback operation failed: %v\n", err)
		}
	}

	if len(failures) > 0 {
		progressf("\n⚠️  WARNING: %d rollback operation(s) failed\n", len(failures))
		progressln("   Some Azure resources may require manual cleanup in the Azure Portal")
	} else {
		progressln("✅ Rollback complete")
	}
}
