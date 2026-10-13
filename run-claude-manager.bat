@echo off
rem Launches claude-manager.exe with its raw stderr captured to a file, and
rem restarts it if it dies without meaning to (see the :run loop below).
rem
rem Wails builds the app as a GUI-subsystem binary (no console), so anything
rem the Go runtime writes directly to the OS-level stderr handle -- an
rem unrecovered panic, or a fatal runtime error such as a concurrent map
rem access or stack overflow, none of which go through app.log's slog
rem logger or trigger a Windows crash report -- normally vanishes with zero
rem trace. Redirecting stderr here is what would catch it next time.
setlocal enabledelayedexpansion
set "EXE=%~dp0build\bin\claude-manager.exe"
set "LOGDIR=%USERPROFILE%\.claude-manager\logs"
set "MARKER=%USERPROFILE%\.claude-manager\state\clean-exit"
if not exist "%LOGDIR%" mkdir "%LOGDIR%"

rem Watchdog budget: how many times in a row the app may die unexpectedly
rem before we give up, and how long it must stay up for a run to count as
rem healthy (a crash loop on a broken config would otherwise spin forever).
set "MAX_RESTARTS=10"
set "HEALTHY_SECONDS=60"
set "RESTARTS=0"

if not exist "%EXE%" (
    echo claude-manager.exe not found, updating from GitHub before building...
    pushd "%~dp0"
    rem --autostash keeps local edits (config.toml, this script) out of the
    rem way; --ff-only never creates a merge commit. A failed pull (offline,
    rem diverged history) is not fatal -- we just build what is on disk.
    git pull --ff-only --autostash
    if errorlevel 1 echo WARNING: git pull failed, building the local copy.
    rem Not `wails build`: the Wails v2.12 CLI crashes on Go 1.27+ with
    rem "package ... without types". Bindings are committed, so build directly.
    pushd frontend
    rem The pull may have brought new frontend dependencies.
    call npm install --no-audit --no-fund
    call npm run build
    popd
    go build -tags desktop,production -ldflags "-H windowsgui -w -s" -o build\bin\claude-manager.exe .
    popd
    if not exist "%EXE%" (
        echo Build failed: claude-manager.exe still missing.
        exit /b 1
    )
    rem The pull may have rewritten this very script; cmd reads .bat files
    rem by byte offset, so jump by label instead of falling through.
    goto :launch
)

:launch

rem Relaunch loop. Every session the app runs is tied to its Job Object
rem (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, internal/proc/proc_windows.go), so
rem when the manager dies every session across every project dies with it.
rem Bringing it straight back lets crash recovery resume those sessions
rem instead of leaving the machine idle until somebody notices.
rem
rem A deliberate quit is told apart from a crash by the marker app.go's
rem shutdown writes on its way out (App.markCleanExit): present means the app
rem meant to exit and we stop; absent means it was killed or crashed.

:run
if exist "%MARKER%" del /q "%MARKER%" >nul 2>&1
call :now START

echo ---- %date% %time% ---- >> "%LOGDIR%\crash.log"
"%EXE%" 2>> "%LOGDIR%\crash.log"

if exist "%MARKER%" goto :done

call :now STOP
set /a UPTIME=STOP-START
if %UPTIME% lss 0 set /a UPTIME=UPTIME+86400

if %UPTIME% geq %HEALTHY_SECONDS% (
    rem The run was long enough to be a real session, so this is an isolated
    rem failure rather than a loop -- forgive the earlier attempts.
    set "RESTARTS=0"
)

set /a RESTARTS+=1
if !RESTARTS! gtr %MAX_RESTARTS% (
    echo ---- %date% %time% gave up after %MAX_RESTARTS% failed restarts ---- >> "%LOGDIR%\crash.log"
    echo claude-manager keeps exiting unexpectedly ^(%MAX_RESTARTS% times^). Not restarting again.
    echo See %LOGDIR%\crash.log
    exit /b 1
)

echo ---- %date% %time% unexpected exit after %UPTIME%s, restart !RESTARTS! of %MAX_RESTARTS% ---- >> "%LOGDIR%\crash.log"
rem Breathing room so a failure that is instant does not spin the CPU.
rem Not `timeout`: it refuses to wait when stdin is redirected, which is
rem exactly how this script runs when launched from another process.
ping -n 6 127.0.0.1 >nul 2>&1
goto :run

:done
echo ---- %date% %time% clean exit ---- >> "%LOGDIR%\crash.log"
exit /b 0

rem Seconds since midnight. The 1-prefix trick stops set /a reading
rem zero-padded hours like 08 as invalid octal; the offsets undo it.
:now
for /f "tokens=1-3 delims=:.," %%a in ("%TIME: =0%") do (
    set /a "%~1=(1%%a-100)*3600+(1%%b-100)*60+(1%%c-100)"
)
goto :eof
