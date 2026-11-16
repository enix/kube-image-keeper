//go:build !sleepbins

package synth

// Runnable tells whether the synthetic images hold a real sleep binary.
// Without the sleepbins tag they hold placeholder bytes: they are served and
// pulled like the real ones, but a container cannot start on them. Build with
// `task tester:bins` and `-tags sleepbins` (as `task tester:build` and the
// image do) to embed the binaries of cmd/sleep.
const Runnable = false

// sleepBinaries maps each supported architecture to its placeholder bytes.
var sleepBinaries = map[string][]byte{
	"amd64": []byte("kuik-tester sleep placeholder (amd64)\n"),
	"arm64": []byte("kuik-tester sleep placeholder (arm64)\n"),
}
