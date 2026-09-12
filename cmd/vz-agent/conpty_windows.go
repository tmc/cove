package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errConPTYUnsupported = errors.New("conpty requires windows 10 version 1809 or later")

type conPTY struct {
	input, output *os.File
	pid           int
	mu            sync.Mutex
	jobMu         sync.Mutex
	console       windows.Handle
	process       windows.Handle
	job           windows.Handle
	once          sync.Once
}

func conPTYSize(rows, cols uint32) (windows.Coord, error) {
	if rows == 0 || cols == 0 || rows > 32767 || cols > 32767 {
		return windows.Coord{}, errors.New("terminal dimensions must be between 1 and 32767")
	}
	return windows.Coord{X: int16(cols), Y: int16(rows)}, nil
}

func startConPTY(cmd *exec.Cmd, rows, cols uint32) (_ *conPTY, err error) {
	size, err := conPTYSize(rows, cols)
	if err != nil {
		return nil, err
	}
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	dll := windows.NewLazySystemDLL("kernel32.dll")
	for _, name := range []string{"CreatePseudoConsole", "ResizePseudoConsole", "ClosePseudoConsole"} {
		if err := dll.NewProc(name).Find(); err != nil {
			return nil, fmt.Errorf("%w: %v", errConPTYUnsupported, err)
		}
	}
	p := new(conPTY)
	defer func() {
		if err != nil {
			p.close()
		}
	}()
	var in, out windows.Handle
	var input, output windows.Handle
	if err = windows.CreatePipe(&in, &input, nil, 0); err != nil {
		return nil, fmt.Errorf("create terminal input: %w", err)
	}
	defer windows.CloseHandle(in)
	p.input = os.NewFile(uintptr(input), "conpty-input")
	if err = windows.CreatePipe(&output, &out, nil, 0); err != nil {
		return nil, fmt.Errorf("create terminal output: %w", err)
	}
	defer windows.CloseHandle(out)
	p.output = os.NewFile(uintptr(output), "conpty-output")
	if err = windows.CreatePseudoConsole(size, in, out, 0, &p.console); err != nil {
		return nil, fmt.Errorf("create pseudoconsole: %w", err)
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("create terminal attributes: %w", err)
	}
	defer attrs.Delete()
	// This attribute takes the console handle itself, unlike ordinary pointer-valued attributes.
	if err = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(p.console), unsafe.Sizeof(p.console)); err != nil {
		return nil, fmt.Errorf("set terminal attribute: %w", err)
	}
	path, err := conPTYExecutable(cmd.Path, cmd.Dir)
	if err != nil {
		return nil, err
	}
	app, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("terminal executable: %w", err)
	}
	line := windows.ComposeCommandLine(cmd.Args)
	if cmd.SysProcAttr != nil {
		if cmd.SysProcAttr.Token != 0 || cmd.SysProcAttr.ParentProcess != 0 || len(cmd.SysProcAttr.AdditionalInheritedHandles) != 0 {
			return nil, errors.New("conpty does not support alternate tokens, parent processes, or inherited handles")
		}
		if cmd.SysProcAttr.CmdLine != "" {
			line = cmd.SysProcAttr.CmdLine
		}
	}
	command, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return nil, fmt.Errorf("terminal command: %w", err)
	}
	var dir *uint16
	if cmd.Dir != "" {
		dir, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, fmt.Errorf("terminal directory: %w", err)
		}
	}
	for _, value := range cmd.Env {
		if strings.ContainsRune(value, 0) {
			return nil, errors.New("terminal environment contains nul")
		}
	}
	env, err := conPTYEnvironment(cmd.Environ())
	if err != nil {
		return nil, err
	}
	p.job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create terminal job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, fmt.Errorf("configure terminal job: %w", err)
	}
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))}, ProcThreadAttributeList: attrs.List()}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err = windows.CreateProcess(app, command, nil, nil, false, flags, &env[0], dir, &si.StartupInfo, &pi); err != nil {
		return nil, fmt.Errorf("start terminal process: %w", err)
	}
	p.process, p.pid = pi.Process, int(pi.ProcessId)
	defer windows.CloseHandle(pi.Thread)
	defer func() {
		if err != nil {
			windows.TerminateProcess(pi.Process, 1)
			windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		}
	}()
	if err = windows.AssignProcessToJobObject(p.job, pi.Process); err != nil {
		return nil, fmt.Errorf("assign terminal job: %w", err)
	}
	if _, err = windows.ResumeThread(pi.Thread); err != nil {
		return nil, fmt.Errorf("resume terminal process: %w", err)
	}
	return p, nil
}

func conPTYEnvironment(env []string) ([]uint16, error) {
	env = append([]string(nil), env...)
	sort.Slice(env, func(i, j int) bool { return strings.ToUpper(env[i]) < strings.ToUpper(env[j]) })
	var block []uint16
	for _, v := range env {
		if strings.ContainsRune(v, 0) {
			return nil, errors.New("terminal environment contains nul")
		}
		block = append(block, utf16.Encode([]rune(v))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func (p *conPTY) resize(rows, cols uint32) error {
	size, err := conPTYSize(rows, cols)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console == 0 {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(p.console, size)
}

func (p *conPTY) closeConsole() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console != 0 {
		windows.ClosePseudoConsole(p.console)
		p.console = 0
	}
}

func (p *conPTY) terminate() error {
	p.jobMu.Lock()
	defer p.jobMu.Unlock()
	if p.job == 0 {
		return os.ErrClosed
	}
	return windows.TerminateJobObject(p.job, 1)
}

func (p *conPTY) wait() (int32, error) {
	if _, err := windows.WaitForSingleObject(p.process, windows.INFINITE); err != nil {
		return -1, fmt.Errorf("wait terminal process: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
		return -1, fmt.Errorf("terminal exit code: %w", err)
	}
	return int32(code), nil
}

func (p *conPTY) close() {
	p.once.Do(func() {
		if p.input != nil {
			p.input.Close()
		}
		if p.output != nil {
			p.output.Close()
		}
		p.jobMu.Lock()
		if p.job != 0 {
			windows.CloseHandle(p.job)
			p.job = 0
		}
		p.jobMu.Unlock()
		p.closeConsole()
		if p.process != 0 {
			windows.CloseHandle(p.process)
		}
	})
}

func conPTYExecutable(path, dir string) (string, error) {
	if path == "" {
		return "", errors.New("terminal executable is empty")
	}
	if !filepath.IsAbs(path) {
		if filepath.VolumeName(path) != "" || os.IsPathSeparator(path[0]) {
			return "", errors.New("conpty requires an absolute or directory-relative executable path")
		}
		var err error
		path, err = filepath.Abs(filepath.Join(dir, path))
		if err != nil {
			return "", fmt.Errorf("terminal executable path: %w", err)
		}
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("terminal executable: %w", err)
	}
	return resolved, nil
}
