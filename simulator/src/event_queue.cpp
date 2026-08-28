#include "orbit/simulator/event_queue.hpp"

#include <limits>
#include <stdexcept>
#include <tuple>
#include <utility>

namespace orbit::simulator {

std::uint64_t EventQueue::push(
    const std::uint64_t timestamp_ms,
    const EventType type,
    std::string target) {
  if (next_ordinal_ == std::numeric_limits<std::uint64_t>::max()) {
    throw std::overflow_error("event ordinal exhausted");
  }

  const auto ordinal = next_ordinal_++;
  events_.push(Event{
      .timestamp_ms = timestamp_ms,
      .ordinal = ordinal,
      .type = type,
      .target = std::move(target),
  });
  return ordinal;
}

Event EventQueue::pop() {
  if (events_.empty()) {
    throw std::out_of_range("cannot pop an empty event queue");
  }

  auto event = events_.top();
  events_.pop();
  return event;
}

const Event& EventQueue::top() const {
  if (events_.empty()) {
    throw std::out_of_range("cannot inspect an empty event queue");
  }
  return events_.top();
}

bool EventQueue::empty() const noexcept { return events_.empty(); }

std::size_t EventQueue::size() const noexcept { return events_.size(); }

bool EventQueue::Later::operator()(const Event& lhs, const Event& rhs) const noexcept {
  return std::tie(lhs.timestamp_ms, lhs.ordinal) >
         std::tie(rhs.timestamp_ms, rhs.ordinal);
}

}  // namespace orbit::simulator
