#include "orbit/simulator/schedule.hpp"

#include "orbit/simulator/event_queue.hpp"
#include "orbit/simulator/prng.hpp"

#include <iomanip>
#include <sstream>
#include <stdexcept>

namespace orbit::simulator {
namespace {

constexpr std::uint64_t kTransportTickMS = 1000;

EventType parse_event_type(const std::string& type) {
  if (type == "device_disconnect") {
    return EventType::device_disconnect;
  }
  if (type == "device_reconnect") {
    return EventType::device_reconnect;
  }
  if (type == "gateway_crash") {
    return EventType::gateway_crash;
  }
  if (type == "gateway_recover") {
    return EventType::gateway_recover;
  }
  if (type == "transport_profile") {
    return EventType::transport_profile;
  }
  if (type == "delivery_drop") {
    return EventType::delivery_drop;
  }
  if (type == "delivery_duplicate") {
    return EventType::delivery_duplicate;
  }
  if (type == "ack_drop") {
    return EventType::ack_drop;
  }
  if (type == "ack_duplicate") {
    return EventType::ack_duplicate;
  }
  throw std::invalid_argument("unsupported schedule event type: " + type);
}

std::string event_type_name(const EventType type) {
  switch (type) {
    case EventType::device_disconnect:
      return "device_disconnect";
    case EventType::device_reconnect:
      return "device_reconnect";
    case EventType::gateway_crash:
      return "gateway_crash";
    case EventType::gateway_recover:
      return "gateway_recover";
    case EventType::transport_profile:
      return "transport_profile";
    case EventType::delivery_drop:
      return "delivery_drop";
    case EventType::delivery_duplicate:
      return "delivery_duplicate";
    case EventType::ack_drop:
      return "ack_drop";
    case EventType::ack_duplicate:
      return "ack_duplicate";
  }
  throw std::logic_error("unknown event type");
}

std::uint64_t jitter_ms(Prng& prng, const std::uint64_t maximum) {
  if (maximum == 0) {
    return 0;
  }
  return prng.next_u64() % (maximum + 1);
}

void append_transport_faults(EventQueue& queue, Prng& prng, const Scenario& scenario, const NetworkProfile& profile) {
  for (std::uint64_t tick = 0; tick < scenario.duration_ms; tick += kTransportTickMS) {
    for (const auto& device : scenario.devices) {
      const auto delivery_roll = prng.next_unit_double();
      const auto latency = profile.latency_ms + jitter_ms(prng, profile.jitter_ms);
      if (delivery_roll < profile.delivery_loss_rate) {
        const auto at = tick + latency;
        if (at <= scenario.duration_ms) {
          queue.push(at, EventType::delivery_drop, device);
        }
      } else if (delivery_roll < profile.delivery_loss_rate + profile.duplicate_rate) {
        const auto at = tick + latency;
        if (at <= scenario.duration_ms) {
          queue.push(at, EventType::delivery_duplicate, device);
        }
      }

      const auto ack_roll = prng.next_unit_double();
      const auto ack_latency = profile.latency_ms * 2 + jitter_ms(prng, profile.jitter_ms);
      if (ack_roll < profile.ack_loss_rate) {
        const auto at = tick + ack_latency;
        if (at <= scenario.duration_ms) {
          queue.push(at, EventType::ack_drop, device);
        }
      } else if (ack_roll < profile.ack_loss_rate + profile.duplicate_rate) {
        const auto at = tick + ack_latency;
        if (at <= scenario.duration_ms) {
          queue.push(at, EventType::ack_duplicate, device);
        }
      }
    }
  }
}

void append_json_string(std::ostringstream& out, const std::string& value) {
  out << '"';
  for (const char character : value) {
    switch (character) {
      case '"':
        out << "\\\"";
        break;
      case '\\':
        out << "\\\\";
        break;
      case '\n':
        out << "\\n";
        break;
      case '\r':
        out << "\\r";
        break;
      case '\t':
        out << "\\t";
        break;
      default:
        out << character;
        break;
    }
  }
  out << '"';
}

void append_network_profile(std::ostringstream& out, const NetworkProfile& profile) {
  out << "      \"profile\": {\n";
  out << "        \"latency_ms\": " << profile.latency_ms << ",\n";
  out << "        \"jitter_ms\": " << profile.jitter_ms << ",\n";
  out << std::setprecision(17);
  out << "        \"delivery_loss_rate\": " << profile.delivery_loss_rate << ",\n";
  out << "        \"ack_loss_rate\": " << profile.ack_loss_rate << ",\n";
  out << "        \"duplicate_rate\": " << profile.duplicate_rate << "\n";
  out << "      }\n";
}

}  // namespace

Schedule compile_schedule(const Scenario& scenario) {
  EventQueue queue;
  for (const auto& event : scenario.events) {
    if (event.type == "transport_profile") {
      queue.push(
          event.at_ms,
          EventType::transport_profile,
          event.device_id,
          true,
          event.profile);
      continue;
    }
    const auto type = parse_event_type(event.type);
    if (!event.device_id.empty()) {
      queue.push(event.at_ms, type, event.device_id);
      continue;
    }
    queue.push(event.at_ms, type, event.gateway_id);
  }

  Prng prng(Prng::parse_seed(scenario.seed));
  append_transport_faults(queue, prng, scenario, scenario.network_profile);

  Schedule schedule;
  schedule.scenario_name = scenario.name;
  schedule.scenario_seed = scenario.seed;
  schedule.duration_ms = scenario.duration_ms;

  while (!queue.empty()) {
    const auto popped = queue.pop();
    ScheduleEvent item;
    item.at_ms = popped.timestamp_ms;
    item.ordinal = popped.ordinal;
    item.type = event_type_name(popped.type);
    if (popped.type == EventType::gateway_crash || popped.type == EventType::gateway_recover) {
      item.gateway_id = popped.target;
    } else {
      item.device_id = popped.target;
    }
    item.has_profile = popped.has_profile;
    item.profile = popped.profile;
    schedule.events.push_back(std::move(item));
  }
  return schedule;
}

std::string canonical_schedule_json(const Schedule& schedule) {
  std::ostringstream out;
  out << "{\n";
  out << "  \"schema_version\": ";
  append_json_string(out, schedule.schema_version);
  out << ",\n";
  out << "  \"scenario_name\": ";
  append_json_string(out, schedule.scenario_name);
  out << ",\n";
  out << "  \"scenario_seed\": ";
  append_json_string(out, schedule.scenario_seed);
  out << ",\n";
  out << "  \"prng_algorithm\": ";
  append_json_string(out, schedule.prng_algorithm);
  out << ",\n";
  out << "  \"duration_ms\": " << schedule.duration_ms << ",\n";
  out << "  \"events\": [\n";
  for (std::size_t index = 0; index < schedule.events.size(); ++index) {
    const auto& event = schedule.events[index];
    out << "    {\n";
    out << "      \"at_ms\": " << event.at_ms << ",\n";
    out << "      \"ordinal\": " << event.ordinal << ",\n";
    out << "      \"type\": ";
    append_json_string(out, event.type);
    if (!event.device_id.empty() || !event.gateway_id.empty() || event.has_profile) {
      out << ",\n";
    } else {
      out << "\n";
    }
    if (!event.device_id.empty()) {
      out << "      \"device_id\": ";
      append_json_string(out, event.device_id);
      if (event.has_profile) {
        out << ",\n";
      } else {
        out << "\n";
      }
    } else if (!event.gateway_id.empty()) {
      out << "      \"gateway_id\": ";
      append_json_string(out, event.gateway_id);
      out << "\n";
    }
    if (event.has_profile) {
      append_network_profile(out, event.profile);
    }
    out << "    }";
    if (index + 1 < schedule.events.size()) {
      out << ",";
    }
    out << "\n";
  }
  out << "  ]\n";
  out << "}\n";
  return out.str();
}

}  // namespace orbit::simulator
