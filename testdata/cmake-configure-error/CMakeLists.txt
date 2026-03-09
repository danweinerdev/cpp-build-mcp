cmake_minimum_required(VERSION 3.16)
project(test_configure_error C CXX)
message(FATAL_ERROR "intentional configure failure")
add_executable(main main.cpp)
