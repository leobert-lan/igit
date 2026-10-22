//go:build windows

package security

import (
	"golang.org/x/sys/windows/registry"
)

func getPlatformMachineID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		s, _, err := k.GetStringValue("MachineGuid")
		if err == nil && s != "" {
			return s
		}
	}
	return ""
}
