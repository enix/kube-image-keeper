//go:build sleepbins

package synth

import _ "embed"

//go:generate task -d ../.. bins

// Runnable tells whether the synthetic images hold a real sleep binary.
const Runnable = true

var (
	//go:embed bin/sleep-amd64
	sleepAMD64 []byte
	//go:embed bin/sleep-arm64
	sleepARM64 []byte
)

// sleepBinaries maps each supported architecture to its sleep binary.
var sleepBinaries = map[string][]byte{"amd64": sleepAMD64, "arm64": sleepARM64}
