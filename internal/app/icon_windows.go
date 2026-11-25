//go:build windows

package app

import (
	"log"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1
)

var (
	user32                = syscall.NewLazyDLL("user32.dll")
	shell32               = syscall.NewLazyDLL("shell32.dll")
	procEnumWindows       = user32.NewProc("EnumWindows")
	procGetWindowThreadID = user32.NewProc("GetWindowThreadProcessId")
	procGetClassNameW     = user32.NewProc("GetClassNameW")
	procSendMessageW      = user32.NewProc("SendMessageW")
	procExtractIconExW    = shell32.NewProc("ExtractIconExW")
)

// setWindowIcon applies the executable's icon to the live Wails window. Wails
// does not always inherit the icon from the PE resources for the title bar.
func setWindowIcon() {
	pid := uint32(os.Getpid())
	for attempt := 0; attempt < 50; attempt++ {
		hwnd := findAppWindow(pid)
		if hwnd == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		exe, err := os.Executable()
		if err != nil {
			log.Printf("window icon: %v", err)
			return
		}
		path, err := syscall.UTF16PtrFromString(exe)
		if err != nil {
			log.Printf("window icon path: %v", err)
			return
		}
		var large, small uintptr
		ret, _, _ := procExtractIconExW.Call(
			uintptr(unsafe.Pointer(path)),
			0,
			uintptr(unsafe.Pointer(&large)),
			uintptr(unsafe.Pointer(&small)),
			1,
		)
		if ret == 0 {
			log.Printf("window icon: no icon resource in %s", exe)
			return
		}
		if large != 0 {
			procSendMessageW.Call(hwnd, wmSetIcon, iconBig, large)
		}
		if small != 0 {
			procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
		}
		return
	}
	log.Printf("window icon: main window not found")
}

func findAppWindow(pid uint32) uintptr {
	var found uintptr
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != pid {
			return 1
		}
		var class [64]uint16
		procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		if syscall.UTF16ToString(class[:]) == "wailsWindow" {
			found = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return found
}
