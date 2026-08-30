#include "orbit/simulator/scenario.hpp"

#include <nlohmann/json.hpp>

#include <stdexcept>

namespace orbit::simulator {
namespace {

NetworkProfile parse_network_profile(const nlohmann::json& value) {
  NetworkProfile profile;
  profile.latency_ms = value.at("latency_ms").get<std::uint64_t>();
  profile.jitter_ms = value.at("jitter_ms").get<std::uint64_t>();
  profile.delivery_loss_rate = value.at("delivery_loss_rate").get<double>();
  profile.ack_loss_rate = value.at("ack_loss_rate").get<double>();
  profile.duplicate_rate = value.at("duplicate_rate").get<double>();
  return profile;
}

}  // namespace

Scenario load_scenario_json(const std::string& json_text) {
  const auto document = nlohmann::json::parse(json_text);
  Scenario scenario;
  scenario.schema_version = document.at("schema_version").get<std::string>();
  if (scenario.schema_version != "1") {
    throw std::invalid_argument("unsupported schema_version");
  }
  scenario.name = document.at("name").get<std::string>();
  scenario.seed = document.at("seed").get<std::string>();
  scenario.duration_ms = document.at("duration_ms").get<std::uint64_t>();
  for (const auto& gateway : document.at("topology").at("gateways")) {
    scenario.gateways.push_back(gateway.get<std::string>());
  }
  for (const auto& device : document.at("topology").at("devices")) {
    scenario.devices.push_back(device.get<std::string>());
  }
  scenario.network_profile = parse_network_profile(document.at("network_profile"));
  for (const auto& event : document.at("events")) {
    ScenarioEvent item;
    item.at_ms = event.at("at_ms").get<std::uint64_t>();
    item.type = event.at("type").get<std::string>();
    if (event.contains("device_id")) {
      item.device_id = event.at("device_id").get<std::string>();
    }
    if (event.contains("gateway_id")) {
      item.gateway_id = event.at("gateway_id").get<std::string>();
    }
    if (event.contains("profile")) {
      item.has_profile = true;
      item.profile = parse_network_profile(event.at("profile"));
    }
    scenario.events.push_back(std::move(item));
  }
  return scenario;
}

}  // namespace orbit::simulator
