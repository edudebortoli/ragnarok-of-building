//go:build windows

package main

import (
	"errors"
	"ragnarok-of-building/internal/ui"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// Windows pieces of the program without a console: message boxes, one
// instance at a time and the folder picker (plain system calls).

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	kernel  = syscall.NewLazyDLL("kernel32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")
	ole32   = syscall.NewLazyDLL("ole32.dll")

	procMessageBox      = user32.NewProc("MessageBoxW")
	procFindWindow      = user32.NewProc("FindWindowW")
	procShowWindow      = user32.NewProc("ShowWindow")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procCreateMutex     = kernel.NewProc("CreateMutexW")
	procBrowseForFolder = shell32.NewProc("SHBrowseForFolderW")
	procPathFromIDList  = shell32.NewProc("SHGetPathFromIDListW")
	procCoInitializeEx  = ole32.NewProc("CoInitializeEx")
	procCoUninitialize  = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree   = ole32.NewProc("CoTaskMemFree")
	procGetOpenFileName = syscall.NewLazyDLL("comdlg32.dll").NewProc("GetOpenFileNameW")
	procDwmSetAttribute = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

const (
	mbOK          = 0x0
	mbIconError   = 0x10
	mbIconInfo    = 0x40
	mbTopmost     = 0x40000
	swRestore     = 9
	errAlreadyRun = 183 // ERROR_ALREADY_EXISTS
)

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func messageBox(text string, flags uintptr) {
	procMessageBox.Call(0, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(appTitle))), flags|mbTopmost)
}

// fatal tells the player why the program stopped (there is no console).
func fatal(msg string) {
	messageBox("Não deu para abrir o Ragnarok of Building:\n\n"+msg, mbOK|mbIconError)
}

// singleInstance holds a named mutex for the program's lifetime; false when
// another copy already runs.
func singleInstance() bool {
	h, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(utf16("Local\\RagnarokOfBuilding"))))
	if h == 0 {
		return true // cannot tell: go on
	}
	var errno syscall.Errno
	return !(errors.As(err, &errno) && errno == errAlreadyRun)
}

// openFileDialog asks for a file to open; filter is pairs of name and
// pattern separated by "|" ("Replays (*.rrf)|*.rrf").
func openFileDialog(title, filter string) (string, error) {
	type openFileName struct {
		structSize    uint32
		owner         uintptr
		instance      uintptr
		filter        *uint16
		customFilter  *uint16
		maxCustFilter uint32
		filterIndex   uint32
		file          *uint16
		maxFile       uint32
		fileTitle     *uint16
		maxFileTitle  uint32
		initialDir    *uint16
		title         *uint16
		flags         uint32
		fileOffset    uint16
		fileExtension uint16
		defExt        *uint16
		custData      uintptr
		hook          uintptr
		templateName  *uint16
		reserved      uintptr
		reserved2     uint32
		flagsEx       uint32
	}
	// The filter is NUL-terminated strings, ending with two NULs.
	var f []uint16
	for _, part := range strings.Split(filter, "|") {
		u, _ := syscall.UTF16FromString(part)
		f = append(f, u...)
	}
	f = append(f, 0)
	buf := make([]uint16, 4096)
	owner, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(utf16(appTitle))))
	ofn := openFileName{owner: owner, filter: &f[0], file: &buf[0], maxFile: uint32(len(buf)), title: utf16(title),
		flags: 0x1000 | 0x800 | 0x80000 | 0x8} // FILEMUSTEXIST | PATHMUSTEXIST | EXPLORER | NOCHANGEDIR
	ofn.structSize = uint32(unsafe.Sizeof(ofn))
	done := make(chan struct{})
	var ok uintptr
	go func() { // the dialog wants a COM apartment on its own thread
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		procCoInitializeEx.Call(0, 0x2)
		defer procCoUninitialize.Call()
		ok, _, _ = procGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	}()
	<-done
	if ok == 0 {
		return "", ui.ErrNoFile
	}
	return syscall.UTF16ToString(buf), nil
}

// focusExisting brings the running copy's window to the front.
func focusExisting() {
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(utf16(appTitle))))
	if hwnd == 0 {
		messageBox("O Ragnarok of Building já está aberto.", mbOK|mbIconInfo)
		return
	}
	procShowWindow.Call(hwnd, swRestore)
	procSetForeground.Call(hwnd)
}

// darkTitleBar makes the window's title bar dark or light (Windows 10 2004
// and newer; older ones keep theirs).
func darkTitleBar(on bool) {
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(utf16(appTitle))))
	if hwnd == 0 || procDwmSetAttribute.Find() != nil {
		return
	}
	var v int32
	if on {
		v = 1
	}
	const useImmersiveDarkMode = 20
	procDwmSetAttribute.Call(hwnd, useImmersiveDarkMode, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
}

type browseInfo struct {
	owner       uintptr
	root        uintptr
	displayName *uint16
	title       *uint16
	flags       uint32
	callback    uintptr
	param       uintptr
	image       int32
}

const (
	bifReturnOnlyFSDirs = 0x1
	bifEditBox          = 0x10
	bifNewDialogStyle   = 0x40
)

// pickFolder opens the system's folder picker over the program's window.
var pickFolder = func() (string, error) {
	done := make(chan struct{})
	var dir string
	var perr error
	go func() {
		defer close(done)
		runtime.LockOSThread() // the dialog needs a COM apartment on one thread
		defer runtime.UnlockOSThread()
		procCoInitializeEx.Call(0, 0x2) // COINIT_APARTMENTTHREADED
		defer procCoUninitialize.Call()
		owner, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(utf16(appTitle))))
		name := make([]uint16, syscall.MAX_PATH)
		bi := browseInfo{owner: owner, displayName: &name[0],
			title: utf16("Escolha a pasta do Ragnarok (a que tem o data.grf)"),
			flags: bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle}
		pidl, _, _ := procBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
		if pidl == 0 {
			perr = errors.New("nenhuma pasta escolhida")
			return
		}
		defer procCoTaskMemFree.Call(pidl)
		path := make([]uint16, 32768)
		if ok, _, _ := procPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&path[0]))); ok == 0 {
			perr = errors.New("pasta inválida")
			return
		}
		dir = syscall.UTF16ToString(path)
	}()
	<-done
	return dir, perr
}
