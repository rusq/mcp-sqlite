// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestMaxRowsValidation verifies that the binary exits with a non-zero status
// and an informative message when -max-rows is given a value less than 1.
func TestMaxRowsValidation(t *testing.T) {
	for _, val := range []string{"0", "-1", "-100"} {
		t.Run("max-rows="+val, func(t *testing.T) {
			cmd := exec.Command("go", "run", ".", "-max-rows="+val)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("-max-rows=%s: expected non-zero exit, got success", val)
			}
			if !strings.Contains(string(out), "-max-rows") {
				t.Errorf("-max-rows=%s: expected error message to mention -max-rows, got: %s", val, out)
			}
		})
	}
}
