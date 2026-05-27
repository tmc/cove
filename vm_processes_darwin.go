package main

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	procAllPIDs                 uint32 = 1
	procListPIDsPathExcludeOnly uint32 = 2
	procPIDListFDs              int32  = 1
	procPIDTBSDInfo             int32  = 3
	procPIDFDVnodePathInfo      int32  = 2
	proxFDTypeVnode             uint32 = 1
	procPIDListFDSize                  = 8
	procPIDTBSDInfoSize                = 136
	procPIDFDVnodePathInfoSize         = 1200
	procPathMax                        = 4096
	procVnodePathOffset                = 176
)

type darwinVMProcessCollector struct{}

func defaultVMProcessCollector() vmProcessCollector {
	return darwinVMProcessCollector{}
}

type darwinProcFDInfo struct {
	FD     int32
	FDType uint32
}

type darwinProcBSDInfo struct {
	Flags       uint32
	Status      uint32
	XStatus     uint32
	PID         uint32
	PPID        uint32
	UID         uint32
	GID         uint32
	RUID        uint32
	RGID        uint32
	SVUID       uint32
	SVGID       uint32
	RFU1        uint32
	Comm        [16]byte
	Name        [32]byte
	NFiles      uint32
	PGID        uint32
	PJobc       uint32
	ETDev       uint32
	ETPGID      uint32
	Nice        int32
	StartTVSec  uint64
	StartTVUSec uint64
}

type darwinVnodeFDInfoWithPath struct {
	Prefix [procVnodePathOffset]byte
	Path   [1024]byte
}

var (
	libprocOnce sync.Once
	libprocErr  error

	procListPIDs     func(kind uint32, typeinfo uint32, buffer unsafe.Pointer, buffersize int32) int32
	procListPIDsPath func(kind uint32, typeinfo uint32, path *byte, pathflags uint32, buffer unsafe.Pointer, buffersize int32) int32
	procPIDInfo      func(pid int32, flavor int32, arg uint64, buffer unsafe.Pointer, buffersize int32) int32
	procPIDFDInfo    func(pid int32, fd int32, flavor int32, buffer unsafe.Pointer, buffersize int32) int32
	procPIDPath      func(pid int32, buffer unsafe.Pointer, buffersize uint32) int32
	procName         func(pid int32, buffer unsafe.Pointer, buffersize uint32) int32
)

func ensureLibproc() error {
	libprocOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_LAZY)
		if err != nil {
			libprocErr = fmt.Errorf("load libproc: %w", err)
			return
		}
		purego.RegisterLibFunc(&procListPIDs, lib, "proc_listpids")
		purego.RegisterLibFunc(&procListPIDsPath, lib, "proc_listpidspath")
		purego.RegisterLibFunc(&procPIDInfo, lib, "proc_pidinfo")
		purego.RegisterLibFunc(&procPIDFDInfo, lib, "proc_pidfdinfo")
		purego.RegisterLibFunc(&procPIDPath, lib, "proc_pidpath")
		purego.RegisterLibFunc(&procName, lib, "proc_name")
	})
	return libprocErr
}

func openFileHolderPIDs(path string) ([]int, error) {
	return darwinFileHolders(path)
}

func (darwinVMProcessCollector) CollectVMProcesses(baseDir string, knownVMs []vmProcessVMInfo) ([]vmProcessInfo, error) {
	if err := ensureLibproc(); err != nil {
		return nil, err
	}
	pids, err := darwinListPIDs()
	if err != nil {
		return nil, err
	}
	var procs []vmProcessInfo
	for _, pid := range pids {
		bsd, ok := darwinBSDInfo(pid)
		if !ok {
			continue
		}
		command := darwinProcessCommand(pid, bsd)
		if !isVirtualizationVMProcess(command) {
			continue
		}
		proc := vmProcessInfo{
			PID:     int(pid),
			PPID:    int(bsd.PPID),
			Command: command,
		}
		paths, err := darwinOpenVnodePaths(pid, int(bsd.NFiles))
		if err != nil {
			proc.OpenFilesErr = err
		}
		proc.OpenFiles = vmProcessOpenFiles(baseDir, knownVMs, paths)
		proc.VMDirs = vmDirsForOpenFiles(baseDir, knownVMs, proc.OpenFiles)
		if len(proc.VMDirs) > 0 {
			proc.Source = "open-files"
		}
		if len(proc.VMDirs) == 0 {
			if vm := vmProcessVMForOwnerPID(knownVMs, proc.PPID); vm != nil {
				proc.VMDirs = []string{vm.Path}
				proc.Source = "server-info"
			}
		}
		proc.Status = vmProcessStatus(proc.VMDirs)
		procs = append(procs, proc)
	}
	return procs, nil
}

func darwinListPIDs() ([]int32, error) {
	n := procListPIDs(procAllPIDs, 0, nil, 0)
	if n < 0 {
		return nil, fmt.Errorf("list process IDs")
	}
	count := int(n)/4 + 128
	if count < 512 {
		count = 512
	}
	for attempts := 0; attempts < 4; attempts++ {
		pids := make([]int32, count)
		got := procListPIDs(procAllPIDs, 0, unsafe.Pointer(&pids[0]), int32(len(pids)*4))
		if got < 0 {
			return nil, fmt.Errorf("list process IDs")
		}
		used := int(got) / 4
		if used < len(pids) {
			out := pids[:used]
			j := 0
			for _, pid := range out {
				if pid > 0 {
					out[j] = pid
					j++
				}
			}
			return out[:j], nil
		}
		count *= 2
	}
	return nil, fmt.Errorf("list process IDs: process table changed too quickly")
}

func darwinFileHolders(path string) ([]int, error) {
	if err := ensureLibproc(); err != nil {
		return nil, err
	}
	cpath, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("file holder path: %w", err)
	}
	n := procListPIDsPath(procAllPIDs, 0, cpath, procListPIDsPathExcludeOnly, nil, 0)
	if n < 0 {
		return nil, fmt.Errorf("list file holders for %s", path)
	}
	count := int(n)/4 + 16
	if count < 32 {
		count = 32
	}
	for attempts := 0; attempts < 4; attempts++ {
		pids := make([]int32, count)
		got := procListPIDsPath(procAllPIDs, 0, cpath, procListPIDsPathExcludeOnly, unsafe.Pointer(&pids[0]), int32(len(pids)*4))
		if got < 0 {
			return nil, fmt.Errorf("list file holders for %s", path)
		}
		used := int(got) / 4
		if used < len(pids) {
			out := make([]int, 0, used)
			for _, pid := range pids[:used] {
				if pid > 0 {
					out = append(out, int(pid))
				}
			}
			return out, nil
		}
		count *= 2
	}
	return nil, fmt.Errorf("list file holders for %s: process table changed too quickly", path)
}

func darwinBSDInfo(pid int32) (darwinProcBSDInfo, bool) {
	var info darwinProcBSDInfo
	got := procPIDInfo(pid, procPIDTBSDInfo, 0, unsafe.Pointer(&info), procPIDTBSDInfoSize)
	return info, got == procPIDTBSDInfoSize
}

func darwinProcessCommand(pid int32, info darwinProcBSDInfo) string {
	path := darwinProcPIDPath(pid)
	name := cString(info.Name[:])
	if name == "" {
		name = cString(info.Comm[:])
	}
	switch {
	case path != "" && name != "":
		return path + " " + name
	case path != "":
		return path
	default:
		return name
	}
}

func darwinProcPIDPath(pid int32) string {
	var buf [procPathMax]byte
	if procPIDPath == nil {
		return ""
	}
	if n := procPIDPath(pid, unsafe.Pointer(&buf[0]), uint32(len(buf))); n <= 0 {
		return ""
	}
	return cString(buf[:])
}

func darwinProcName(pid int32) string {
	var buf [64]byte
	if procName == nil {
		return ""
	}
	if n := procName(pid, unsafe.Pointer(&buf[0]), uint32(len(buf))); n <= 0 {
		return ""
	}
	return cString(buf[:])
}

func darwinOpenVnodePaths(pid int32, nfiles int) ([]string, error) {
	count := nfiles + 16
	if count < 64 {
		count = 64
	}
	var lastErr error
	for attempts := 0; attempts < 4; attempts++ {
		fds := make([]darwinProcFDInfo, count)
		got := procPIDInfo(pid, procPIDListFDs, 0, unsafe.Pointer(&fds[0]), int32(len(fds)*procPIDListFDSize))
		if got < 0 {
			return nil, fmt.Errorf("list open files for pid %d", pid)
		}
		used := int(got) / procPIDListFDSize
		if used >= len(fds) {
			count *= 2
			continue
		}
		var paths []string
		for _, fd := range fds[:used] {
			if fd.FDType != proxFDTypeVnode {
				continue
			}
			path, ok := darwinVnodeFDPath(pid, fd.FD)
			if ok {
				paths = append(paths, path)
			}
		}
		return paths, lastErr
	}
	return nil, fmt.Errorf("list open files for pid %d: descriptor table changed too quickly", pid)
}

func darwinVnodeFDPath(pid, fd int32) (string, bool) {
	var info darwinVnodeFDInfoWithPath
	got := procPIDFDInfo(pid, fd, procPIDFDVnodePathInfo, unsafe.Pointer(&info), procPIDFDVnodePathInfoSize)
	if got != procPIDFDVnodePathInfoSize {
		return "", false
	}
	path := cString(info.Path[:])
	return path, path != ""
}

func cString(buf []byte) string {
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}
