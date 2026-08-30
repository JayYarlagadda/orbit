#ifndef ORBIT_SIMULATOR_SCENARIO_HPP
#define ORBIT_SIMULATOR_SCENARIO_HPP

#include "orbit/simulator/event_queue.hpp"

#include <cstdint>
#include <string>
#include <vector>

namespace orbit::simulator {

struct ScenarioEvent final {
  std::uint64_t at_ms{};
  std::string type;
  std::string device_id;
  std::string gateway_id;
  bool has_profile{};
  NetworkProfile profile{};
};

struct Scenario final {
  std::string schema_version;
  std::string name;
  std::string seed;
  std::uint64_t duration_ms{};
  std::vector<std::string> gateways;
  std::vector<std::string> devices;
  NetworkProfile network_profile;
  std::vector<ScenarioEvent> events;
};

[[nodiscard]] Scenario load_scenario_json(const std::string& json_text);

}  // namespace orbit::simulator

#endif  // ORBIT_SIMULATOR_SCENARIO_HPP
