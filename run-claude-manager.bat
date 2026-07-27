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
    echo claude-manager.exe not found, building...
    pushd "%~dp0"
    call wails build
    popd
    if not exist "%EXE%" (
        echo Build failed: claude-manager.exe still missing.
        exit /b 1
    )
)

echo ---- %date% %time% ---- >> "%LOGDIR%\crash.log"
"%EXE%" 2>> "%LOGDIR%\crash.log"
