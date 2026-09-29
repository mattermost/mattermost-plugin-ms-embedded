// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"os"
)

// Progress, prompts, and warnings go to stderr so stdout carries only the
// command's machine-readable output.

func progress(args ...any) {
	fmt.Fprint(os.Stderr, args...)
}

func progressln(args ...any) {
	fmt.Fprintln(os.Stderr, args...)
}

func progressf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}
