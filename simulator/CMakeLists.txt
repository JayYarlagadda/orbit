cmake_minimum_required(VERSION 3.28)

include(FetchContent)
FetchContent_Declare(
  nlohmann_json
  GIT_REPOSITORY https://github.com/nlohmann/json.git
  GIT_TAG v3.11.3
)
FetchContent_MakeAvailable(nlohmann_json)

add_library(orbit_simulator STATIC)

target_sources(
  orbit_simulator
  PRIVATE
    src/event_queue.cpp
    src/prng.cpp
    src/scenario.cpp
    src/schedule.cpp
  PUBLIC
    FILE_SET HEADERS
    BASE_DIRS include
    FILES
      include/orbit/simulator/event_queue.hpp
      include/orbit/simulator/prng.hpp
      include/orbit/simulator/scenario.hpp
      include/orbit/simulator/schedule.hpp
)

target_compile_features(orbit_simulator PUBLIC cxx_std_20)
target_link_libraries(orbit_simulator PRIVATE nlohmann_json::nlohmann_json)

if(MSVC)
  target_compile_options(orbit_simulator PRIVATE /W4 /permissive-)
  if(ORBIT_WARNINGS_AS_ERRORS)
    target_compile_options(orbit_simulator PRIVATE /WX)
  endif()
else()
  target_compile_options(
    orbit_simulator
    PRIVATE
      -Wall
      -Wextra
      -Wpedantic
      -Wconversion
      -Wsign-conversion
  )
  if(ORBIT_WARNINGS_AS_ERRORS)
    target_compile_options(orbit_simulator PRIVATE -Werror)
  endif()
endif()

if(ORBIT_ENABLE_SANITIZERS)
  if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU" AND NOT WIN32)
    target_compile_options(orbit_simulator PUBLIC -fsanitize=address,undefined -fno-omit-frame-pointer)
    target_link_options(orbit_simulator PUBLIC -fsanitize=address,undefined)
  else()
    message(FATAL_ERROR "ORBIT_ENABLE_SANITIZERS requires Clang or GCC on a non-Windows platform")
  endif()
endif()

if(BUILD_TESTING)
  add_executable(orbit_simulator_tests
    tests/main.cpp
    tests/event_queue_test.cpp
    tests/prng_test.cpp
    tests/schedule_test.cpp
  )
  target_link_libraries(orbit_simulator_tests PRIVATE orbit_simulator)
  target_compile_features(orbit_simulator_tests PRIVATE cxx_std_20)

  if(MSVC)
    target_compile_options(orbit_simulator_tests PRIVATE /W4 /permissive-)
    if(ORBIT_WARNINGS_AS_ERRORS)
      target_compile_options(orbit_simulator_tests PRIVATE /WX)
    endif()
  else()
    target_compile_options(orbit_simulator_tests PRIVATE -Wall -Wextra -Wpedantic -Wconversion -Wsign-conversion)
    if(ORBIT_WARNINGS_AS_ERRORS)
      target_compile_options(orbit_simulator_tests PRIVATE -Werror)
    endif()
  endif()

  add_test(NAME orbit_simulator.event_queue COMMAND orbit_simulator_tests event_queue)
  add_test(NAME orbit_simulator.prng COMMAND orbit_simulator_tests prng)
  add_test(NAME orbit_simulator.schedule COMMAND orbit_simulator_tests schedule)
  set_tests_properties(
    orbit_simulator.event_queue
    orbit_simulator.prng
    orbit_simulator.schedule
    PROPERTIES WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}
  )
endif()
