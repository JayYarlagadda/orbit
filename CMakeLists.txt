cmake_minimum_required(VERSION 3.28)

project(
  orbit
  VERSION 0.1.0
  DESCRIPTION "Fault-tolerant command delivery for intermittent edge networks"
  LANGUAGES CXX
)

include(CTest)

option(ORBIT_ENABLE_SANITIZERS "Enable AddressSanitizer and UndefinedBehaviorSanitizer" OFF)
option(ORBIT_WARNINGS_AS_ERRORS "Treat compiler warnings as errors" ON)

add_subdirectory(simulator)
