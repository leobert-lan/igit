//go:build !windows

package security

import "os"

func getPlatformMachineID() string {
	if b, err := os.ReadFile("/etc/machine-id"); err == nil && len(b) > 0 {
		return string(b)
	}
	return ""
}
