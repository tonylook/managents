#include "managents/core/format.hpp"

#include <cstdio>

namespace managents::core {
namespace {

constexpr std::uint32_t kSecondsPerMinute = 60;
constexpr std::uint32_t kSecondsPerHour = 3600;
constexpr std::int64_t kSecondsPerDay = 86400;

}  // namespace

void formatAge(std::uint32_t seconds, char* out, std::size_t capacity) {
    if (seconds < kSecondsPerMinute) {
        std::snprintf(out, capacity, "%lus", static_cast<unsigned long>(seconds));
    } else if (seconds < kSecondsPerHour) {
        std::snprintf(out, capacity, "%lum", static_cast<unsigned long>(seconds / kSecondsPerMinute));
    } else {
        std::snprintf(out, capacity, "%luh %lum", static_cast<unsigned long>(seconds / kSecondsPerHour),
                      static_cast<unsigned long>((seconds % kSecondsPerHour) / kSecondsPerMinute));
    }
}

void formatClock(std::int64_t localEpochSeconds, char* out, std::size_t capacity) {
    std::int64_t secondOfDay = localEpochSeconds % kSecondsPerDay;
    if (secondOfDay < 0) {
        secondOfDay += kSecondsPerDay;
    }
    const int hours = static_cast<int>(secondOfDay / kSecondsPerHour);
    const int minutes = static_cast<int>((secondOfDay % kSecondsPerHour) / kSecondsPerMinute);
    std::snprintf(out, capacity, "%02d:%02d", hours, minutes);
}

std::uint8_t percentOf(std::uint32_t used, std::uint32_t limit) {
    if (limit == 0 || used >= limit) {
        return limit == 0 ? 0 : 100;
    }
    return static_cast<std::uint8_t>((static_cast<std::uint64_t>(used) * 100 + limit / 2) / limit);
}

}  // namespace managents::core
