// Minimal dedicated worker binary for process isolation.
// Build: go build -o wasmer-process-worker ./cmd/wasmer-process-worker
package main

import (
	"os"

	wasmer "chainmaker.org/chainmaker/vm-wasmer/v2"
)

func main() {
	os.Setenv("WASMER_PROCESS_WORKER", "1")
	os.Exit(wasmer.RunProcessIsolationWorker())
}
