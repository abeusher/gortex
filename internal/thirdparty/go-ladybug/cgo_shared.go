package lbug

//go:generate bash ../../../scripts/fetch-lbug.sh

/*
// liblbug is fetched by scripts/fetch-lbug.sh (not committed).
//
// linux + darwin: STATIC — liblbug.a is linked in (only the archive
// lives in lib/static/<os>-<arch>/, so `-llbug` resolves to it) for a
// self-contained binary with no runtime lib to ship. The C++ runtime is
// linked too: libc++ on darwin (system, always present); libstdc++ +
// libgcc statically on linux so the binary doesn't need them at runtime.
//
// windows: DYNAMIC — lbug's windows release is MSVC-built (its C++
// runtime is MSVCP140/VCRUNTIME140), which cannot be statically linked
// into a mingw binary. The .exe links directly against lbug_shared.dll
// (mingw ld reads the DLL's clean C ABI export table via -l:<file>, so
// no import lib / gendef is needed) and ships the DLL — plus the VC++
// runtime — alongside the .exe at runtime.
// -rdynamic: liblbug loads its FTS (and other) extensions via dlopen at
// runtime, and those extension .so/.dylibs resolve liblbug's C++ symbols
// (e.g. lbug::catalog::IndexAuxInfo typeinfo) FROM THE HOST PROCESS. When
// liblbug is a shared lib those symbols are globally visible; static-
// linked, they must be forced into the binary's dynamic symbol table or
// the extension fails with "undefined symbol" at load time. -rdynamic is
// the portable driver flag (clang -> -export_dynamic, gcc ->
// --export-dynamic) and is on cgo's LDFLAGS allowlist. Required on both
// unix targets.
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/lib/static/darwin-amd64 -llbug -lc++ -rdynamic
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/lib/static/darwin-arm64 -llbug -lc++ -rdynamic
// libstdc++ is wrapped in -Wl,-Bstatic/-Bdynamic (NOT -static-libstdc++):
// cgo may link the final binary with the C driver (gcc), which never
// auto-appends libstdc++, so -static-libstdc++ could be a no-op and the
// explicit -lstdc++ would resolve to libstdc++.so.6 at runtime —
// defeating the self-contained goal. -Bstatic forces the .a. libm/dl/
// pthread stay dynamic (system libs always present); libgcc is statically
// linked via -static-libgcc. --export-dynamic exposes liblbug's symbols
// for the dlopen'd FTS extension (see darwin note above).
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/lib/static/linux-amd64 -llbug -Wl,-Bstatic -lstdc++ -Wl,-Bdynamic -lm -ldl -lpthread -static-libgcc -rdynamic
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/lib/static/linux-arm64 -llbug -Wl,-Bstatic -lstdc++ -Wl,-Bdynamic -lm -ldl -lpthread -static-libgcc -rdynamic
#cgo windows LDFLAGS: -L${SRCDIR}/lib/dynamic/windows -l:lbug_shared.dll
#include "lbug.h"
*/
import "C"
