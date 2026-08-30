#ifndef ORBIT_SIMULATOR_SCHEDULE_HPP
#define ORBIT_SIMULATOR_SCHEDULE_HPP

#include "orbit/simulator/prng.hpp"
#include "orbit/simulator/scenario.hpp"

#include <cstdint>
#include <string>
#include <vector>

namespace orbit::simulator {

struct ScheduleEvent final {
  std::uint64_t at_ms{};
  std::uint64_t ordinal{};
  std::string type;
  std::string device_id;
  std::string gateway_id;
  bool has_profile{};
  NetworkProfile profile{};
};

struct Schedule final {
  std::string schema_version{"1"};
  std::string scenario_name;
  std::string scenario_seed;
  std::string prng_algorithm{kPrngAlgorithm};
  std::uint64_t duration_ms{};
  std::vector<ScheduleEvent> events;
};

[[nodiscard]] Schedule compile_schedule(const Scenario& scenario);
[[nodiscard]] std::string canonical_schedule_json(const Schedule& schedule);

}  // namespace orbit::simulator

#endif  // ORBIT_SIMULATOR_SCHEDULE_HPP
