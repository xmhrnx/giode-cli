#!/bin/sh
# Builds the context menu handler DLLs the giode CLI embeds, for x64 and
# ARM64, freestanding (no C runtime), with clang, lld and llvm-dlltool
# and the MinGW-w64 headers (Debian and Ubuntu: clang lld llvm
# mingw-w64). Run it here after changing contextmenu.c.
set -e
cd "$(dirname "$0")"
inc=${MINGW_INCLUDE:-/usr/share/mingw-w64/include}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/kernel32.def" <<DEF
LIBRARY kernel32.dll
EXPORTS
CloseHandle
CreateFileW
CreateProcessW
GetFileSize
GetModuleFileNameW
GetProcessHeap
HeapAlloc
HeapFree
MultiByteToWideChar
ReadFile
DEF
cat > "$tmp/ole32.def" <<DEF
LIBRARY ole32.dll
EXPORTS
CoTaskMemAlloc
CoTaskMemFree
DEF
printf 'EXPORTS\nDllGetClassObject\nDllCanUnloadNow\n' > "$tmp/exports.def"
for arch in x64 arm64; do
	case $arch in
	x64) target=x86_64-w64-windows-gnu; machine=i386:x86-64 ;;
	arm64) target=aarch64-w64-windows-gnu; machine=arm64 ;;
	esac
	for lib in kernel32 ole32; do
		llvm-dlltool -m $machine -d "$tmp/$lib.def" -l "$tmp/lib$lib-$arch.a"
	done
	clang --target=$target -isystem "$inc" -O2 -Wall -Wno-unused-parameter -ffreestanding -fno-stack-protector \
		-nostdlib -shared -fuse-ld=lld -Wl,--entry=DllMainCRTStartup -Wl,--no-insert-timestamp -s \
		-o contextmenu_$arch.dll contextmenu.c "$tmp/exports.def" "$tmp/libkernel32-$arch.a" "$tmp/libole32-$arch.a"
done
ls -l contextmenu_*.dll
