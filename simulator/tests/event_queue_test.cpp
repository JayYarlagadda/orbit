#include "orbit/simulator/event_queue.hpp"

#include <cstdlib>
#include <exception>
#include <iostream>
#include <stdexcept>
#include <string_view>

namespace {

using orbit::simulator::EventQueue;
using orbit::simulator::EventType;

void require(const bool condition, const std::string_view message) {
  if (!condition) {
    throw std::runtime_error(std::string{message});
  }
}

void orders_by_timestamp() {
  EventQueue queue;
  static_cast<void>(queue.push(20, EventType::device_reconnect, "device-a"));
  static_cast<void>(queue.push(10, EventType::device_disconnect, "device-a"));

  require(queue.pop().timestamp_ms == 10, "earliest event must be first");
  require(queue.pop().timestamp_ms == 20, "latest event must be last");
  require(queue.empty(), "queue must be empty after both events are consumed");
}

void preserves_insertion_order_for_ties() {
  EventQueue queue;
  const auto first = queue.push(10, EventType::gateway_crash, "gateway-a");
  const auto second = queue.push(10, EventType::gateway_recover, "gateway-a");

  require(first == 0, "first ordinal must be zero");
  require(second == 1, "second ordinal must be one");
  require(queue.pop().ordinal == first, "first tied event must preserve document order");
  require(queue.pop().ordinal == second, "second tied event must preserve document order");
}

void rejects_empty_access() {
  EventQueue queue;
  try {
    static_cast<void>(queue.top());
    throw std::runtime_error("top on an empty queue did not throw");
  } catch (const std::out_of_range&) {
  }

  try {
    static_cast<void>(queue.pop());
    throw std::runtime_error("pop on an empty queue did not throw");
  } catch (const std::out_of_range&) {
  }
}

}  // namespace

int main() {
  try {
    orders_by_timestamp();
    preserves_insertion_order_for_ties();
    rejects_empty_access();
    return EXIT_SUCCESS;
  } catch (const std::exception& error) {
    std::cerr << "event_queue_test: " << error.what() << '\n';
    return EXIT_FAILURE;
  }
}
