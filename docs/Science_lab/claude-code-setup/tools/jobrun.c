/* jobrun.c — запуск процесса в Windows Job Object (аналог cgroups).
 *   jobrun MEM_MB TIMEOUT_MS cmdline...
 * Лимит памяти на процесс, один активный процесс (нельзя плодить дочерние),
 * kill-on-job-close, жёсткий таймаут. Печатает в stderr итог. */
#include <windows.h>
#include <stdio.h>
#include <string.h>

int main(int argc, char **argv) {
    if (argc < 4) return 2;
    SIZE_T mem = (SIZE_T)atoi(argv[1]) << 20;
    DWORD timeout = (DWORD)atoi(argv[2]);
    /* склеиваем командную строку из оставшихся аргументов как есть */
    char *full = GetCommandLineA(), *p = full;
    int skip = 3; int inq = 0;
    while (*p && skip) { if (*p == '"') inq = !inq; else if (*p == ' ' && !inq) { while (*p == ' ') p++; skip--; continue; } p++; }

    HANDLE job = CreateJobObjectA(NULL, NULL);
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION li = {0};
    li.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_PROCESS_MEMORY | JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
                                          JOB_OBJECT_LIMIT_ACTIVE_PROCESS | JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION;
    li.BasicLimitInformation.ActiveProcessLimit = 1;
    li.ProcessMemoryLimit = mem;
    SetInformationJobObject(job, JobObjectExtendedLimitInformation, &li, sizeof li);

    STARTUPINFOA si = {sizeof si}; PROCESS_INFORMATION pi;
    if (!CreateProcessA(NULL, p, NULL, NULL, TRUE, CREATE_SUSPENDED, NULL, NULL, &si, &pi)) {
        fprintf(stderr, "jobrun: CreateProcess failed %lu\n", GetLastError()); return 3;
    }
    AssignProcessToJobObject(job, pi.hProcess);
    ResumeThread(pi.hThread);
    DWORD w = WaitForSingleObject(pi.hProcess, timeout);
    DWORD code = 0;
    if (w == WAIT_TIMEOUT) { TerminateJobObject(job, 124); WaitForSingleObject(pi.hProcess, INFINITE); fprintf(stderr, "jobrun: TIMEOUT\n"); return 124; }
    GetExitCodeProcess(pi.hProcess, &code);
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION after; QueryInformationJobObject(job, JobObjectExtendedLimitInformation, &after, sizeof after, NULL);
    fprintf(stderr, "jobrun: exit=%lu peak_MB=%.1f\n", code, after.PeakProcessMemoryUsed / 1048576.0);
    return (int)code;
}
