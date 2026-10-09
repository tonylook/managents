#pragma once

#include <cstddef>
#include <cstdint>

namespace managents::core {

/// "42s", "5m", "1h 12m" (the POC's format). Always NUL-terminates `out`.
void formatAge(std::uint32_t seconds, char* out, std::size_t capacity);

/// 24h "HH:MM" for a local time expressed as seconds since the epoch.
void formatClock(std::int64_t localEpochSeconds, char* out, std::size_t capacity);

/// Rounded percentage of `used` over `limit`, clamped to 0..100. `limit` must be > 0.
std::uint8_t percentOf(std::uint32_t used, std::uint32_t limit);

}  // namespace managents::core
