# DiagnosticFormat.cmake — Detect and configure structured diagnostic output.
#
# Probes the active C and C++ compilers for structured diagnostic format
# support and sets variables describing what was found. Designed to be
# injected into a project via CMAKE_PROJECT_INCLUDE by cpp-build-mcp.
#
# After inclusion the following variables are available:
#
#   CPP_BUILD_MCP_DIAG_SUPPORTED  - TRUE if a structured format is available
#   CPP_BUILD_MCP_DIAG_FORMAT     - "sarif", "json", or ""
#   CPP_BUILD_MCP_DIAG_C_FLAGS    - flags to pass to the C compiler (list)
#   CPP_BUILD_MCP_DIAG_CXX_FLAGS  - flags to pass to the C++ compiler (list)
#
# The flags are automatically appended to CMAKE_C_FLAGS / CMAKE_CXX_FLAGS.

if(DEFINED _CPP_BUILD_MCP_DIAG_FORMAT_INCLUDED)
    return()
endif()
set(_CPP_BUILD_MCP_DIAG_FORMAT_INCLUDED TRUE)

include(CheckCCompilerFlag)
include(CheckCXXCompilerFlag)

# Preserve any existing CMAKE_REQUIRED_FLAGS so the probe doesn't leak.
set(_DIAG_SAVED_REQUIRED_FLAGS "${CMAKE_REQUIRED_FLAGS}")

set(CPP_BUILD_MCP_DIAG_SUPPORTED FALSE)
set(CPP_BUILD_MCP_DIAG_FORMAT    "")
set(CPP_BUILD_MCP_DIAG_C_FLAGS   "")
set(CPP_BUILD_MCP_DIAG_CXX_FLAGS "")

if(MSVC)
    # MSVC output is already machine-parseable — no flag needed.
    message(STATUS "[cpp-build-mcp] Diagnostic format: none (MSVC uses native output)")
    set(CMAKE_REQUIRED_FLAGS "${_DIAG_SAVED_REQUIRED_FLAGS}")
    return()
endif()

# ── Try GCC-style JSON first (-fdiagnostics-format=json, GCC 10+) ──────────
check_c_compiler_flag("-fdiagnostics-format=json" _DIAG_C_JSON)
check_cxx_compiler_flag("-fdiagnostics-format=json" _DIAG_CXX_JSON)

if(_DIAG_C_JSON AND _DIAG_CXX_JSON)
    set(CPP_BUILD_MCP_DIAG_SUPPORTED TRUE)
    set(CPP_BUILD_MCP_DIAG_FORMAT    "json")
    set(CPP_BUILD_MCP_DIAG_C_FLAGS   "-fdiagnostics-format=json")
    set(CPP_BUILD_MCP_DIAG_CXX_FLAGS "-fdiagnostics-format=json")

    string(APPEND CMAKE_C_FLAGS   " -fdiagnostics-format=json")
    string(APPEND CMAKE_CXX_FLAGS " -fdiagnostics-format=json")

    message(STATUS "[cpp-build-mcp] Diagnostic format: json")
    set(CMAKE_REQUIRED_FLAGS "${_DIAG_SAVED_REQUIRED_FLAGS}")
    return()
endif()

# ── Try Clang-style SARIF (-fdiagnostics-format=sarif, Clang 14+) ──────────
# Suppress -Wsarif-format-unstable during the probe so -Werror doesn't
# reject the flag (mirrors Fusion's approach).
set(CMAKE_REQUIRED_FLAGS "${_DIAG_SAVED_REQUIRED_FLAGS} -Wno-sarif-format-unstable")
check_c_compiler_flag("-fdiagnostics-format=sarif" _DIAG_C_SARIF)
check_cxx_compiler_flag("-fdiagnostics-format=sarif" _DIAG_CXX_SARIF)
set(CMAKE_REQUIRED_FLAGS "${_DIAG_SAVED_REQUIRED_FLAGS}")

if(_DIAG_C_SARIF AND _DIAG_CXX_SARIF)
    set(CPP_BUILD_MCP_DIAG_SUPPORTED TRUE)
    set(CPP_BUILD_MCP_DIAG_FORMAT    "sarif")
    set(CPP_BUILD_MCP_DIAG_C_FLAGS   "-fdiagnostics-format=sarif;-Wno-sarif-format-unstable")
    set(CPP_BUILD_MCP_DIAG_CXX_FLAGS "-fdiagnostics-format=sarif;-Wno-sarif-format-unstable")

    string(APPEND CMAKE_C_FLAGS   " -fdiagnostics-format=sarif -Wno-sarif-format-unstable")
    string(APPEND CMAKE_CXX_FLAGS " -fdiagnostics-format=sarif -Wno-sarif-format-unstable")

    message(STATUS "[cpp-build-mcp] Diagnostic format: sarif")
    return()
endif()

# ── Neither format supported ────────────────────────────────────────────────
message(STATUS "[cpp-build-mcp] Diagnostic format: none (compiler supports neither json nor sarif)")
