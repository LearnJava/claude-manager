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

rem Auto-update on every start: fetch origin, fast-forward if it is ahead,
rem and rebuild when the checked-out commit differs from the one the exe was
rem built from (recorded in STAMP). Outside a git checkout, or offline, this
rem is skipped and the existing exe is used. Delete STAMP to force a rebuild
rem (e.g. after uncommitted local edits, which are not detected).
rem
rem The whole step is one parenthesised block on purpose: a pull may rewrite
rem this very script, and cmd reads .bat files by byte offset, so the block
rem is parsed before the pull and ends in a jump by label.
set "STAMP=%~dp0build\bin\claude-manager.commit"
pushd "%~dp0"
git rev-parse --git-dir >nul 2>&1
if errorlevel 1 (
    popd
    goto :launch
)
(
    git fetch --quiet origin >nul 2>&1
    set "BEHIND=0"
    for /f %%n in ('git rev-list --count HEAD..@{u} 2^>nul') do set "BEHIND=%%n"
    if !BEHIND! gtr 0 (
        echo Updating: !BEHIND! new commit^(s^) on origin...
        rem --autostash keeps local edits out of the way; --ff-only never
        rem creates a merge commit. A failed pull is not fatal.
        git pull --ff-only --autostash
        if errorlevel 1 echo WARNING: git pull failed, keeping the local copy.
    )
    set "HEADREV="
    for /f %%h in ('git rev-parse HEAD') do set "HEADREV=%%h"
    set "BUILT="
    if exist "%STAMP%" set /p BUILT=<"%STAMP%"
    set "NEEDBUILD="
    if not exist "%EXE%" set "NEEDBUILD=1"
    if not "!HEADREV!"=="!BUILT!" set "NEEDBUILD=1"
    if defined NEEDBUILD (
        echo Building claude-manager at !HEADREV!...
        rem `wails build`, never a bare `go build`: it regenerates the frontend
        rem bindings - frontend/wailsjs is not in git, so stale bindings break
        rem the frontend build once a new bound method lands - and embeds the
        rem Windows resources: icon, manifest with per-monitor DPI awareness and
        rem Common Controls v6, version info. It runs npm install and npm run
        rem build itself. The CLI must match the Wails version in go.mod, v2.16:
        rem 2.12 crashes on Go 1.27. -nosyncgomod keeps go.mod untouched.
        where wails >nul 2>&1
        if errorlevel 1 for /f "delims=" %%g in ('go env GOPATH') do set "PATH=!PATH!;%%g\bin"
        where wails >nul 2>&1
        if errorlevel 1 (
            echo WARNING: wails CLI not found, falling back to go build - no fresh bindings, icon or manifest.
            echo Install it: go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
            pushd frontend
            call npm install --no-audit --no-fund
            call npm run build
            popd
            go build -tags desktop,production -ldflags "-H windowsgui -w -s" -o build\bin\claude-manager.exe .
        ) else (
            wails build -nosyncgomod
        )
        if errorlevel 1 (
            echo WARNING: build failed.
        ) else (
            >"%STAMP%" echo !HEADREV!
        )
        rem cm-mcp (the MCP server clients launch over stdio) is rebuilt with the app,
        rem otherwise clients keep running a stale binary with an outdated tool list.
        rem A failure (e.g. a client holds the exe open) only prints a warning.
        go build -o build\cm-mcp.exe ./cmd/cm-mcp
        if errorlevel 1 echo WARNING: cm-mcp build failed, clients keep the previous build\cm-mcp.exe.
        rem wails build rewrites go.mod with LF line endings even with
        rem -nosyncgomod. When its content is unchanged, put the checkout copy
        rem back so git status stays clean; real local edits are left alone.
        git diff --quiet -- go.mod
        if not errorlevel 1 git checkout -q -- go.mod
    )
    popd
    if not exist "%EXE%" (
        echo Build failed: claude-manager.exe still missing.
        exit /b 1
    )
    goto :launch
)

:launch
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
