#pragma once

#include <cstdint>

#include "managents/core/model.hpp"

// Colours (RGB888) and the status palette, kept identical to the agent-lights
// POC so both look the same side by side.

namespace managents::ui {

namespace color {
constexpr std::uint32_t kBackground = 0x09090B;
constexpr std::uint32_t kMutedText = 0x71717A;
constexpr std::uint32_t kFaintText = 0x3F3F46;
constexpr std::uint32_t kBrightText = 0xE5E7EB;
constexpr std::uint32_t kLogoTile = 0x131010;
constexpr std::uint32_t kClaude = 0xD97757;
constexpr std::uint32_t kOpenCodeOuter = 0xFFFFFF;
constexpr std::uint32_t kOpenCodeInner = 0x5A5858;
constexpr std::uint32_t kBarTrack = 0x000000;
constexpr std::uint32_t kBarOk = 0xE5E7EB;
constexpr std::uint32_t kBarWarn = 0xF97316;
constexpr std::uint32_t kBarCritical = 0x7F1D1D;
}  // namespace color

struct CardPalette {
    std::uint32_t background;
    std::uint32_t text;
    bool dimLogo;
};

inline CardPalette paletteFor(core::AgentStatus status, bool alertPhase) {
    switch (status) {
        case core::AgentStatus::Working:
            return {0x16A34A, 0xFFFFFF, false};
        case core::AgentStatus::Waiting:
            return {0xFACC15, 0x1C1917, false};
        case core::AgentStatus::Error:
            return {alertPhase ? 0x7F1D1DU : 0xDC2626U, 0xFFFFFF, false};
        case core::AgentStatus::Idle:
            break;
    }
    return {0x1C1C1F, color::kMutedText, true};
}

inline const char* labelFor(core::AgentStatus status) {
    switch (status) {
        case core::AgentStatus::Working:
            return "WORKING";
        case core::AgentStatus::Waiting:
            return "WAITING";
        case core::AgentStatus::Error:
            return "ERROR";
        case core::AgentStatus::Idle:
            break;
    }
    return "IDLE";
}

/// Context bar colour: shifts toward warning above 70% and 90%.
inline std::uint32_t contextBarColor(std::uint8_t percent) {
    if (percent >= 90) {
        return color::kBarCritical;
    }
    return percent >= 70 ? color::kBarWarn : color::kBarOk;
}

}  // namespace managents::ui
