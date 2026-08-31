cmake_minimum_required(VERSION 3.20.0)

find_package(Zephyr REQUIRED HINTS $ENV{ZEPHYR_BASE})
project(orbit_e1)

list(APPEND DTS_ROOT ${CMAKE_CURRENT_SOURCE_DIR}/..)

target_sources(app PRIVATE
	src/main.c
	${CMAKE_CURRENT_SOURCE_DIR}/../acquisition/orbit_acquisition.c
	${CMAKE_CURRENT_SOURCE_DIR}/../drivers/imu/lsm6dso_orbit.c
)

zephyr_include_directories(
	${CMAKE_CURRENT_SOURCE_DIR}/../acquisition
	${CMAKE_CURRENT_SOURCE_DIR}/../drivers/imu
)

set(DTC_OVERLAY_FILE ${CMAKE_CURRENT_SOURCE_DIR}/../boards/nucleo_h743zi.overlay)
