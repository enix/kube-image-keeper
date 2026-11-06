package synth

import "fmt"

// SleepBinary returns the sleep binary for arch ("amd64" or "arm64"): the
// static one built from cmd/sleep with the sleepbins tag, placeholder bytes
// without it (see Runnable).
func SleepBinary(arch string) ([]byte, error) {
	bin, ok := sleepBinaries[arch]
	if !ok {
		return nil, fmt.Errorf("unsupported architecture %q", arch)
	}
	return bin, nil
}
