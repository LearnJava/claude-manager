@echo off
rem Launches claude-manager.exe with its raw stderr captured to a file.
rem
rem Wails builds the app as a GUI-subsystem binary (no console), so anything
rem the Go runtime writes directly to the OS-level stderr handle -- an
rem unrecovered panic, or a fatal runtime error such as a concurrent map
rem access or stack overflow, none of which go through app.log's slog
rem logger or trigger a Windows crash report -- normally vanishes with zero
rem trace. Redirecting stderr here is what would catch it next time.
setlocal
set "EXE=%~dp0build\bin\claude-manager.exe"
set "LOGDIR=%USERPROFILE%\.claude-manager\logs"
if not exist "%LOGDIR%" mkdir "%LOGDIR%"

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

echo ---- %date% %time% ---- >> "%LOGDIR%\crash.log"
"%EXE%" 2>> "%LOGDIR%\crash.log"
