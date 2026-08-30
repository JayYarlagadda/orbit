#ifndef ORBIT_SIMULATOR_PRNG_HPP
#define ORBIT_SIMULATOR_PRNG_HPP

#include <cstdint>
#include <string>

namespace orbit::simulator {

inline constexpr char kPrngAlgorithm[] = "splitmix64-v1";

// Prng implements the scenario seed stream using SplitMix64. The algorithm
// version is recorded in canonical schedules so cross-language replay stays
// aligned with the Go reference compiler.
class Prng final {
 public:
  explicit Prng(std::uint64_t seed) noexcept;

  [[nodiscard]] std::uint64_t next_u64() noexcept;
  [[nodiscard]] double next_unit_double() noexcept;

  static std::uint64_t parse_seed(const std::string& seed);

 private:
  std::uint64_t state_;
};

}  // namespace orbit::simulator

#endif  // ORBIT_SIMULATOR_PRNG_HPP
