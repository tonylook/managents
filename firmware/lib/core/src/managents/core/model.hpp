#pragma once

#include <cstddef>
#include <cstdint>

#include "managents/core/fixed_string.hpp"

namespace managents::core {

/// Maximum number of agents a single `state` frame may carry (see docs/protocol.md).
inline constexpr std::size_t kMaxAgents = 24;

enum class AgentKind : std::uint8_t { Unknown, Claude, OpenCode };

enum class AgentStatus : std::uint8_t { Working, Waiting, Error, Idle };

/// Context-window usage reported by the host. `limit == 0` means "limit unknown".
struct ContextUsage {
    bool known = false;
    std::uint32_t used = 0;
    std::uint32_t limit = 0;

    bool operator==(const ContextUsage& other) const {
        return known == other.known && used == other.used && limit == other.limit;
    }
    bool operator!=(const ContextUsage& other) const { return !(*this == other); }
};

struct Agent {
    FixedString<48> id;
    FixedString<64> name;
    AgentKind kind = AgentKind::Unknown;
    AgentStatus status = AgentStatus::Waiting;
    std::uint32_t ageSeconds = 0;
    ContextUsage context;

    bool operator==(const Agent& other) const {
        return id == other.id && name == other.name && kind == other.kind && status == other.status &&
               ageSeconds == other.ageSeconds && context == other.context;
    }
    bool operator!=(const Agent& other) const { return !(*this == other); }
};

/// The last valid `state` frame received from the host.
struct HostState {
    std::int64_t hostEpochSeconds = 0;
    std::int32_t utcOffsetSeconds = 0;
    Agent agents[kMaxAgents];
    std::uint8_t agentCount = 0;
    std::uint16_t moreCount = 0;
};

}  // namespace managents::core
