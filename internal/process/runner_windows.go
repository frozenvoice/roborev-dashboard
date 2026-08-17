//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	createNoWindow  = 0x08000000
	createSuspended = 0x00000004
)

func prepareCommand(cmd *exec.Cmd, suspended bool) {
	flags := uint32(createNoWindow)
	if suspended {
		flags |= createSuspended
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
}

const (
	jobObjectInfoClassExtendedLimit = 9
	jobObjectLimitKillOnJobClose    = 0x00002000
	processTerminate                = 0x0001
	processSetQuota                 = 0x0100
	processSuspendResume            = 0x0800
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	openProcess              = kernel32.NewProc("OpenProcess")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObject       = kernel32.NewProc("TerminateJobObject")
	closeHandle              = kernel32.NewProc("CloseHandle")
	ntResumeProcess          = syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IOInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type windowsJob struct {
	handle        uintptr
	processHandle uintptr
}

func winCallError(name string, callErr error) error {
	if callErr == nil || callErr == syscall.Errno(0) {
		return fmt.Errorf("%s failed", name)
	}
	return fmt.Errorf("%s: %w", name, callErr)
}

func attachProcessTree(cmd *exec.Cmd) (processTree, error) {
	job, _, callErr := createJobObjectW.Call(0, 0)
	if job == 0 {
		return nil, winCallError("CreateJobObjectW", callErr)
	}
	closeJob := true
	defer func() {
		if closeJob {
			_, _, _ = closeHandle.Call(job)
		}
	}()

	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	ok, _, callErr := setInformationJobObject.Call(job, jobObjectInfoClassExtendedLimit, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if ok == 0 {
		return nil, winCallError("SetInformationJobObject", callErr)
	}

	processHandle, _, callErr := openProcess.Call(processTerminate|processSetQuota|processSuspendResume, 0, uintptr(cmd.Process.Pid))
	if processHandle == 0 {
		return nil, winCallError("OpenProcess", callErr)
	}
	closeProcess := true
	defer func() {
		if closeProcess {
			_, _, _ = closeHandle.Call(processHandle)
		}
	}()
	ok, _, callErr = assignProcessToJobObject.Call(job, processHandle)
	if ok == 0 {
		return nil, winCallError("AssignProcessToJobObject", callErr)
	}
	closeJob = false
	closeProcess = false
	return &windowsJob{handle: job, processHandle: processHandle}, nil
}

func (j *windowsJob) Resume() error {
	if j.processHandle == 0 {
		return nil
	}
	status, _, _ := ntResumeProcess.Call(j.processHandle)
	_, _, _ = closeHandle.Call(j.processHandle)
	j.processHandle = 0
	if status != 0 {
		return fmt.Errorf("NtResumeProcess failed with status 0x%x", status)
	}
	return nil
}

func (j *windowsJob) Terminate() error {
	if j.handle == 0 {
		return nil
	}
	ok, _, callErr := terminateJobObject.Call(j.handle, 1)
	if ok == 0 {
		return winCallError("TerminateJobObject", callErr)
	}
	return nil
}

func (j *windowsJob) Close() error {
	if j.processHandle != 0 {
		_, _, _ = closeHandle.Call(j.processHandle)
		j.processHandle = 0
	}
	if j.handle == 0 {
		return nil
	}
	handle := j.handle
	j.handle = 0
	ok, _, callErr := closeHandle.Call(handle)
	if ok == 0 {
		return winCallError("CloseHandle", callErr)
	}
	return nil
}
