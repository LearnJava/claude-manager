"""jobrun.py - run a command inside a Windows Job Object (portable fallback for jobrun.exe).

    python jobrun.py MEM_MB TIMEOUT_MS program [args...]

Limits: per-process memory cap, one active process (no child processes), kill on close,
hard timeout. Exit code: the child's exit code; 124 on timeout. Pure Python + ctypes,
no compiler needed. Same command-line contract as jobrun.exe (built from jobrun.c).
"""
import ctypes
import subprocess
import sys
from ctypes import wintypes

k32 = ctypes.WinDLL("kernel32", use_last_error=True)
ntdll = ctypes.WinDLL("ntdll")

JobObjectExtendedLimitInformation = 9
JOB_OBJECT_LIMIT_PROCESS_MEMORY = 0x00000100
JOB_OBJECT_LIMIT_ACTIVE_PROCESS = 0x00000008
JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000
JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION = 0x00000400
CREATE_SUSPENDED = 0x00000004


class IO_COUNTERS(ctypes.Structure):
    _fields_ = [(n, ctypes.c_ulonglong) for n in (
        "ReadOperationCount", "WriteOperationCount", "OtherOperationCount",
        "ReadTransferCount", "WriteTransferCount", "OtherTransferCount")]


class BASIC_LIMIT(ctypes.Structure):
    _fields_ = [("PerProcessUserTimeLimit", ctypes.c_longlong),
                ("PerJobUserTimeLimit", ctypes.c_longlong),
                ("LimitFlags", wintypes.DWORD),
                ("MinimumWorkingSetSize", ctypes.c_size_t),
                ("MaximumWorkingSetSize", ctypes.c_size_t),
                ("ActiveProcessLimit", wintypes.DWORD),
                ("Affinity", ctypes.c_size_t),
                ("PriorityClass", wintypes.DWORD),
                ("SchedulingClass", wintypes.DWORD)]


class EXTENDED_LIMIT(ctypes.Structure):
    _fields_ = [("BasicLimitInformation", BASIC_LIMIT),
                ("IoInfo", IO_COUNTERS),
                ("ProcessMemoryLimit", ctypes.c_size_t),
                ("JobMemoryLimit", ctypes.c_size_t),
                ("PeakProcessMemoryUsed", ctypes.c_size_t),
                ("PeakJobMemoryUsed", ctypes.c_size_t)]


k32.CreateJobObjectW.restype = wintypes.HANDLE
k32.CreateJobObjectW.argtypes = [wintypes.LPVOID, wintypes.LPCWSTR]
k32.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, wintypes.LPVOID, wintypes.DWORD]
k32.QueryInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, wintypes.LPVOID, wintypes.DWORD, wintypes.LPVOID]
k32.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
k32.TerminateJobObject.argtypes = [wintypes.HANDLE, wintypes.UINT]
ntdll.NtResumeProcess.argtypes = [wintypes.HANDLE]


def main() -> int:
    if len(sys.argv) < 4:
        print(__doc__, file=sys.stderr)
        return 2
    mem_mb, timeout_ms, cmd = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3:]
    job = k32.CreateJobObjectW(None, None)
    if not job:
        raise ctypes.WinError(ctypes.get_last_error())
    info = EXTENDED_LIMIT()
    info.BasicLimitInformation.LimitFlags = (JOB_OBJECT_LIMIT_PROCESS_MEMORY | JOB_OBJECT_LIMIT_ACTIVE_PROCESS
                                             | JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
                                             | JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION)
    info.BasicLimitInformation.ActiveProcessLimit = 1
    info.ProcessMemoryLimit = mem_mb << 20
    if not k32.SetInformationJobObject(job, JobObjectExtendedLimitInformation, ctypes.byref(info), ctypes.sizeof(info)):
        raise ctypes.WinError(ctypes.get_last_error())
    proc = subprocess.Popen(cmd, creationflags=CREATE_SUSPENDED)
    handle = wintypes.HANDLE(int(proc._handle))
    if not k32.AssignProcessToJobObject(job, handle):
        proc.kill()
        raise ctypes.WinError(ctypes.get_last_error())
    ntdll.NtResumeProcess(handle)
    try:
        code = proc.wait(timeout=timeout_ms / 1000)
    except subprocess.TimeoutExpired:
        k32.TerminateJobObject(job, 124)
        proc.wait()
        print("jobrun: TIMEOUT", file=sys.stderr)
        return 124
    after = EXTENDED_LIMIT()
    k32.QueryInformationJobObject(job, JobObjectExtendedLimitInformation, ctypes.byref(after), ctypes.sizeof(after), None)
    print(f"jobrun: exit={code} peak_MB={after.PeakProcessMemoryUsed / 1048576:.1f}", file=sys.stderr)
    return code


if __name__ == "__main__":
    sys.exit(main())
