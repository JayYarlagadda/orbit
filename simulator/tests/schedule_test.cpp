#include "orbit/simulator/scenario.hpp"
#include "orbit/simulator/schedule.hpp"

#include <cstdlib>
#include <exception>
#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <string_view>

namespace {

using orbit::simulator::canonical_schedule_json;
using orbit::simulator::compile_schedule;
using orbit::simulator::load_scenario_json;

void require(const bool condition, const std::string_view message) {
  if (!condition) {
    throw std::runtime_error(std::string{message});
  }
}

std::string read_file(const std::string& path) {
  std::ifstream input(path);
  if (!input) {
    throw std::runtime_error("unable to open " + path);
  }
  std::ostringstream buffer;
  buffer << input.rdbuf();
  return buffer.str();
}

void offline_reconnect_matches_golden() {
  const auto scenario_text = read_file("scenarios/examples/offline-reconnect.v1.json");
  const auto golden_text = read_file("scenarios/golden/offline-reconnect.v1.schedule.json");
  const auto scenario = load_scenario_json(scenario_text);
  const auto schedule = compile_schedule(scenario);
  const auto actual = canonical_schedule_json(schedule);
  require(actual == golden_text, "offline reconnect schedule does not match the golden fixture");
}

void schedule_events_are_monotonic() {
  const auto scenario_text = read_file("scenarios/examples/offline-reconnect.v1.json");
  const auto scenario = load_scenario_json(scenario_text);
  const auto schedule = compile_schedule(scenario);
  for (std::size_t index = 1; index < schedule.events.size(); ++index) {
    const auto& previous = schedule.events[index - 1];
    const auto& current = schedule.events[index];
    require(
        current.at_ms > previous.at_ms ||
            (current.at_ms == previous.at_ms && current.ordinal > previous.ordinal),
        "schedule events must be monotonic by timestamp and ordinal");
  }
}

}  // namespace

int run_schedule_tests() {
  offline_reconnect_matches_golden();
  schedule_events_are_monotonic();
  return EXIT_SUCCESS;
}
