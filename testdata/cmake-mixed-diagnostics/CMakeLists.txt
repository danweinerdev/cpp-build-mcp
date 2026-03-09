cmake_minimum_required(VERSION 3.16)
project(test_mixed C CXX)
add_executable(main good.cpp bad.cpp)
target_compile_options(main PRIVATE -Wall)
