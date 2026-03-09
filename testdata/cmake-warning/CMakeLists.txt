cmake_minimum_required(VERSION 3.16)
project(test_warning C CXX)
add_executable(main main.cpp)
target_compile_options(main PRIVATE -Wall)
