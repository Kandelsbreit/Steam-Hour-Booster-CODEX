//go:build windows

package platform

import (
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"unsafe"
)

func DataDir() (string, error) {
	d, err := os.UserConfigDir()
	return filepath.Join(d, "agnia-steam-hours"), err
}
func AutoLaunch(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		err = key.DeleteValue("AgniaSteamHours")
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue("AgniaSteamHours", `"`+exe+`"`)
}

var wininet = windows.NewLazySystemDLL("wininet.dll").NewProc("InternetGetConnectedState")
var processMemory = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func Online() bool {
	var flags uint32
	r, _, _ := wininet.Call(uintptr(unsafe.Pointer(&flags)), 0)
	return r != 0
}
func MemoryMB() uint64 {
	var m struct {
		Size, Faults                                                uint32
		Peak, Working, PoolPeak, Pool, NonPeak, Non, Page, PagePeak uintptr
	}
	m.Size = uint32(unsafe.Sizeof(m))
	r, _, _ := processMemory.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&m)), uintptr(m.Size))
	if r == 0 {
		return 0
	}
	return uint64(m.Working) / 1048576
}
func ErrorBox(message string) {
	p, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("Agnia Steam Hours")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(title)), 0x10)
}
