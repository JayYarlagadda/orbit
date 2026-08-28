#ifndef ORBIT_SIMULATOR_EVENT_QUEUE_HPP
#define ORBIT_SIMULATOR_EVENT_QUEUE_HPP

#include <cstddef>
#include <cstdint>
#include <queue>
#include <string>
#include <vector>

namespace orbit::simulator {

enum class EventType : std::uint8_t {
  device_disconnect,
  device_reconnect,
  gateway_crash,
  gateway_recover,
  transport_profile,
};

struct Event final {
  std::uint64_t timestamp_ms{};
  std::uint64_t ordinal{};
  EventType type{};
  std::string target;

  friend bool operator==(const Event&, const Event&) = default;
};

// EventQueue orders by logical timestamp and then insertion ordinal. The
// ordinal is the cross-language tie-breaker required by scenario schema v1.
class EventQueue final {
 public:
  EventQueue() = default;

  [[nodiscard]] std::uint64_t push(
      std::uint64_t timestamp_ms,
      EventType type,
      std::string target);
  [[nodiscard]] Event pop();
  [[nodiscard]] const Event& top() const;
  [[nodiscard]] bool empty() const noexcept;
  [[nodiscard]] std::size_t size() const noexcept;

 private:
  struct Later final {
    [[nodiscard]] bool operator()(const Event& lhs, const Event& rhs) const noexcept;
  };

  std::priority_queue<Event, std::vector<Event>, Later> events_;
  std::uint64_t next_ordinal_{};
};

}  // namespace orbit::simulator

#endif  // ORBIT_SIMULATOR_EVENT_QUEUE_HPP
