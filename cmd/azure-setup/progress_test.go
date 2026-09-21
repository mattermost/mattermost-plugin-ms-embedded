// Copyright (c) 2025-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStreams runs fn with both standard streams redirected and returns what
// each received.
func captureStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	defer func() {
		os.Stdout, os.Stderr = origOut, origErr
	}()

	fn()

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	var outBuf, errBuf bytes.Buffer
	_, err = io.Copy(&outBuf, outR)
	require.NoError(t, err)
	_, err = io.Copy(&errBuf, errR)
	require.NoError(t, err)

	return outBuf.String(), errBuf.String()
}

func TestProgressWritesToStderr(t *testing.T) {
	stdout, stderr := captureStreams(t, func() {
		progress("prompt: ")
		progressln("a line")
		progressf("formatted %d\n", 42)
	})

	assert.Empty(t, stdout, "progress output must never reach stdout")
	assert.Contains(t, stderr, "prompt: ")
	assert.Contains(t, stderr, "a line")
	assert.Contains(t, stderr, "formatted 42")
}

// TestOnlyResultRenderersWriteToStdout guards the contract that makes
// `doctor -o json` and `create -o env` pipeable: stdout carries the result
// document and nothing else, so progress and warnings cannot corrupt it.
func TestOnlyResultRenderersWriteToStdout(t *testing.T) {
	// Renderers legitimately write the result document to stdout. doctor.go is
	// allowed one os.Stdout reference, where it hands the writer to the report
	// renderer.
	allowed := map[string]bool{
		"output.go":        true,
		"doctor_report.go": true,
		"doctor.go":        true,
		"progress.go":      true,
	}

	out, err := exec.Command("grep", "-rln", "-e", `fmt\.Print`, "-e", `os\.Stdout`, ".").Output()
	require.NoError(t, err)

	for file := range strings.FieldsSeq(string(out)) {
		file = strings.TrimPrefix(file, "./")
		if strings.HasSuffix(file, "_test.go") || allowed[file] {
			continue
		}

		t.Errorf("%s writes to stdout; progress and diagnostics belong on stderr via the progress helpers", file)
	}
}
