#include "orbit/simulator/prng.hpp"

#include <cstdlib>
#include <exception>
#include <iostream>
#include <string_view>

namespace {

using orbit::simulator::Prng;

void require(const bool condition, const std::string_view message) {
  if (!condition) {
    throw std::runtime_error(std::string{message});
  }
}

void splitmix64_is_deterministic() {
  Prng first(42);
  Prng second(42);
  for (int index = 0; index < 8; ++index) {
    require(first.next_u64() == second.next_u64(), "splitmix64 stream diverged for the same seed");
  }
}

void parse_seed_rejects_invalid_values() {
  try {
    static_cast<void>(Prng::parse_seed("+1"));
    throw std::runtime_error("parse_seed accepted a leading plus sign");
  } catch (const std::invalid_argument&) {
  }
}

}  // namespace

int run_prng_tests() {
  splitmix64_is_deterministic();
  parse_seed_rejects_invalid_values();
  return EXIT_SUCCESS;
}
