#include "orbit/simulator/prng.hpp"

#include <cmath>
#include <limits>
#include <stdexcept>

namespace orbit::simulator {

Prng::Prng(const std::uint64_t seed) noexcept : state_{seed} {}

std::uint64_t Prng::next_u64() noexcept {
  std::uint64_t value = (state_ += 0x9e3779b97f4a7c15ULL);
  value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9ULL;
  value = (value ^ (value >> 27)) * 0x94d049bb133111ebULL;
  return value ^ (value >> 31);
}

double Prng::next_unit_double() noexcept {
  return static_cast<double>(next_u64() >> 11) *
         (1.0 / static_cast<double>(1ULL << 53));
}

std::uint64_t Prng::parse_seed(const std::string& seed) {
  if (seed.empty() || seed.front() == '+' ||
      (seed.size() > 1 && seed.front() == '0')) {
    throw std::invalid_argument("seed must be a canonical unsigned decimal string");
  }
  std::size_t consumed = 0;
  const auto value = std::stoull(seed, &consumed);
  if (consumed != seed.size()) {
    throw std::invalid_argument("seed must be a canonical unsigned decimal string");
  }
  return value;
}

}  // namespace orbit::simulator
