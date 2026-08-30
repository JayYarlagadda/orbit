#include <cstdlib>
#include <exception>
#include <iostream>
#include <string_view>

int run_event_queue_tests();
int run_prng_tests();
int run_schedule_tests();

namespace {

void require(const bool condition, const std::string_view message) {
  if (!condition) {
    throw std::runtime_error(std::string{message});
  }
}

}  // namespace

int main(const int argc, char** argv) {
  try {
    if (argc == 1) {
      require(run_event_queue_tests() == EXIT_SUCCESS, "event_queue tests failed");
      require(run_prng_tests() == EXIT_SUCCESS, "prng tests failed");
      require(run_schedule_tests() == EXIT_SUCCESS, "schedule tests failed");
      return EXIT_SUCCESS;
    }
    if (std::string_view{argv[1]} == "event_queue") {
      return run_event_queue_tests();
    }
    if (std::string_view{argv[1]} == "prng") {
      return run_prng_tests();
    }
    if (std::string_view{argv[1]} == "schedule") {
      return run_schedule_tests();
    }
    std::cerr << "unknown test suite: " << argv[1] << '\n';
    return EXIT_FAILURE;
  } catch (const std::exception& error) {
    std::cerr << "orbit_simulator_tests: " << error.what() << '\n';
    return EXIT_FAILURE;
  }
}
