// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"os"
)

// Progress, prompts, and warnings go to stderr so that stdout carries only the
// command's actual output: a JSON or Markdown doctor report, the env exports,
// or the Mattermost config fragment. Without this, `doctor -o json -v` puts
// "Authenticating to Azure..." ahead of the document and breaks any consumer
// piping it into jq.
//
// Only the renderers in output.go and doctor_report.go write to stdout.

func progress(args ...any) {
	fmt.Fprint(os.Stderr, args...)
}

func progressln(args ...any) {
	fmt.Fprintln(os.Stderr, args...)
}

func progressf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}
