//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	trayWindowClass = "DsxIDTrayWindow"
	trayCallbackMsg = wmApp + 1

	wmApp            = 0x8000
	wmLButtonUp      = 0x0202
	wmLButtonDblClk  = 0x0203
	wmRButtonUp      = 0x0205
	wmContextMenu    = 0x007B
	wmClose          = 0x0010
	wmDestroy        = 0x0002
	wmCommand        = 0x0111
	wmNull           = 0x0000
	ninSelect        = 0x0400
	ninKeySelect     = 0x0401
	nimAdd           = 0
	nimDelete        = 2
	nimSetVersion    = 4
	nifMessage       = 0x00000001
	nifIcon          = 0x00000002
	nifTip           = 0x00000004
	nifShowTip       = 0x00000080
	notifyIconVer4   = 4
	mfString         = 0x00000000
	mfSeparator      = 0x00000800
	tpmReturnCmd     = 0x0100
	tpmRightButton   = 0x0002
	trayOpenID       = 1
	trayLockID       = 2
	trayQuitID       = 3
	idcArrow         = 32512
	hwndMessageValue = ^uintptr(2) // HWND_MESSAGE (-3)
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenuW      = user32.NewProc("AppendMenuW")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSetForegroundWin = user32.NewProc("SetForegroundWindow")
	procDestroyIcon      = user32.NewProc("DestroyIcon")
	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	trayWndProcCallback = syscall.NewCallback(trayWndProc)
	trayMu              sync.Mutex
	trayHWND            uintptr
	trayStarted         bool
	trayOpenAction      func()
	trayLockAction      func()
	trayQuitAction      func()
)

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type winPoint struct {
	X int32
	Y int32
}

type winMsg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       winPoint
	LPrivate uint32
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

// startTray creates the Windows notification-area icon and its hidden message
// window. The callbacks are invoked from the tray thread.
func startTray(onOpen, onLock, onQuit func()) error {
	trayMu.Lock()
	if trayStarted {
		trayMu.Unlock()
		return nil
	}
	trayStarted = true
	trayOpenAction = onOpen
	trayLockAction = onLock
	trayQuitAction = onQuit
	trayMu.Unlock()

	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := runTray(ready); err != nil {
			select {
			case ready <- err:
			default:
			}
		}
	}()
	select {
	case err := <-ready:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("tray icon did not start in time")
	}
}

// stopTray removes the tray icon and ends its message loop.
func stopTray() {
	trayMu.Lock()
	hwnd := trayHWND
	trayHWND = 0
	trayMu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
}

func runTray(ready chan<- error) error {
	className, err := syscall.UTF16PtrFromString(trayWindowClass)
	if err != nil {
		return err
	}
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	if hInstance == 0 {
		return errors.New("could not get the application instance")
	}
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   trayWndProcCallback,
		HInstance:     hInstance,
		HCursor:       cursor,
		LpszClassName: className,
	}
	// A zero result can mean the class already exists; window creation below
	// reports any real registration failure.
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0,
		0,
		0, 0, 0, 0,
		hwndMessageValue,
		0,
		hInstance,
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("could not create the tray window: %v", callErr)
	}
	trayMu.Lock()
	trayHWND = hwnd
	trayMu.Unlock()
	defer func() {
		trayMu.Lock()
		if trayHWND == hwnd {
			trayHWND = 0
		}
		trayMu.Unlock()
		procDestroyWindow.Call(hwnd)
	}()

	icon, err := extractTrayIcon()
	if err != nil {
		return err
	}
	defer procDestroyIcon.Call(icon)

	nid := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip | nifShowTip,
		UCallbackMessage: trayCallbackMsg,
		HIcon:            icon,
	}
	copyUTF16(nid.SzTip[:], "DsxID - local identity")
	if !shellNotifyIcon(nimAdd, &nid) {
		return errors.New("could not add the tray icon")
	}
	defer shellNotifyIcon(nimDelete, &nid)
	nid.UVersion = notifyIconVer4
	shellNotifyIcon(nimSetVersion, &nid)

	// The icon is live now; the application can finish starting while the
	// tray thread continues its message loop below.
	ready <- nil

	var msg winMsg
	for {
		ret, _, msgErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) == -1 {
			return fmt.Errorf("tray message loop failed: %v", msgErr)
		}
		if ret == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func extractTrayIcon() (uintptr, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	path, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
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
		return 0, errors.New("the executable has no icon for the tray")
	}
	if small != 0 {
		if large != 0 {
			procDestroyIcon.Call(large)
		}
		return small, nil
	}
	return large, nil
}

func shellNotifyIcon(cmd uint32, nid *notifyIconData) bool {
	ret, _, _ := procShellNotifyIconW.Call(uintptr(cmd), uintptr(unsafe.Pointer(nid)))
	return ret != 0
}

func copyUTF16(dst []uint16, value string) {
	src, err := syscall.UTF16FromString(value)
	if err != nil {
		return
	}
	copy(dst, src)
}

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case trayCallbackMsg:
		switch uint32(lparam & 0xffff) {
		case wmLButtonUp, wmLButtonDblClk, ninSelect, ninKeySelect:
			runTrayAction(trayOpenID)
		case wmRButtonUp, wmContextMenu:
			showTrayMenu(hwnd)
		}
		return 0
	case wmCommand:
		runTrayAction(uintptr(wparam & 0xffff))
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return ret
}

func runTrayAction(id uintptr) {
	trayMu.Lock()
	action := func() {}
	switch id {
	case trayOpenID:
		action = trayOpenAction
	case trayLockID:
		action = trayLockAction
	case trayQuitID:
		action = trayQuitAction
	}
	trayMu.Unlock()
	if action != nil {
		go action()
	}
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	openLabel, _ := syscall.UTF16PtrFromString("Open DsxID")
	lockLabel, _ := syscall.UTF16PtrFromString("Lock identity")
	quitLabel, _ := syscall.UTF16PtrFromString("Quit DsxID")
	procAppendMenuW.Call(menu, mfString, trayOpenID, uintptr(unsafe.Pointer(openLabel)))
	procAppendMenuW.Call(menu, mfString, trayLockID, uintptr(unsafe.Pointer(lockLabel)))
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	procAppendMenuW.Call(menu, mfString, trayQuitID, uintptr(unsafe.Pointer(quitLabel)))

	var pt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWin.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmReturnCmd|tpmRightButton,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		hwnd,
		0,
	)
	procPostMessageW.Call(hwnd, wmNull, 0, 0)
	if cmd != 0 {
		runTrayAction(cmd)
	}
}
