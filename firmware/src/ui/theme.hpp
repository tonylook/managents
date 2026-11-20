#pragma once

#include <cstddef>
#include <cstdint>

#include "managents/core/model.hpp"

// Colours and the status styles. Every colour is a typed RGB888 constant:
// LovyanGFX reads a plain int literal as RGB565 and would draw another colour.
// Names, labels and screen text keep at least 4.5:1 contrast with what is
// behind them, so they stay readable on a TN panel seen at an angle. The card
// age, drawn a step dimmer, keeps at least 3.8:1 (white on the error red).

namespace managents::ui {

namespace color {
constexpr std::uint32_t kBackground = 0x09090B;
constexpr std::uint32_t kBrightText = 0xE5E7EB;
constexpr std::uint32_t kMutedText = 0xA1A1AA;
constexpr std::uint32_t kFaintText = 0x52525B;  ///< decoration only: the wordmark, other pages' dots, the footer
constexpr std::uint32_t kQrBackdrop = 0xFFFFFF;

constexpr std::uint32_t kWorking = 0x15803D;
constexpr std::uint32_t kWaiting = 0xFACC15;
constexpr std::uint32_t kError = 0xDC2626;
constexpr std::uint32_t kErrorDark = 0x7F1D1D;  ///< the dark phase of a blinking error card
constexpr std::uint32_t kIdle = 0x27272A;
constexpr std::uint32_t kOnDark = 0xFFFFFF;
constexpr std::uint32_t kOnLight = 0x1C1917;

constexpr std::uint32_t kLogoTile = 0x131010;
constexpr std::uint32_t kClaude = 0xD97757;
constexpr std::uint32_t kOpenCodeOuter = 0xFFFFFF;
constexpr std::uint32_t kOpenCodeInner = 0x5A5858;
constexpr std::uint32_t kCodexTop = 0xB1A7FF;  ///< the Codex gradient, top to bottom
constexpr std::uint32_t kCodexMiddle = 0x7A9DFF;
constexpr std::uint32_t kCodexBottom = 0x3941FF;
constexpr std::uint32_t kCodexPrompt = 0xFFFFFF;
constexpr std::uint32_t kZCode = 0xFFFFFF;      ///< the Z.ai mark is monochrome: white on the tile
constexpr std::uint32_t kQwen = 0xA78BFA;       ///< a stylised Q for Qwen Code, violet like its brand
constexpr std::uint32_t kGeminiTop = 0x8AB4FF;  ///< the Gemini spark, a blue gradient top to bottom
constexpr std::uint32_t kGeminiBottom = 0x3B6EFF;
constexpr std::uint32_t kPi = 0x3FD4C0;     ///< the pi mark, a lowercase pi in teal
constexpr std::uint32_t kDroid = 0xD6DEEA;  ///< the droid robot head, light steel
constexpr std::uint32_t kCrush = 0xF25FA0;  ///< the Crush heart, Charmbracelet pink
constexpr std::uint32_t kGoose = 0xFFFFFF;  ///< the goose is a white bird on the tile
constexpr std::uint32_t kGooseBeak = 0xF2A33C;
constexpr std::uint32_t kAmp = 0xFFB020;  ///< the Amp bolt, amber
constexpr std::uint32_t kOmp = 0x79E28C;  ///< Oh My Pi: the pi glyph with a spark, mint green
constexpr std::uint32_t kBlack = 0x000000;
}  // namespace color

/// Mixes `percent` (0..100) of `toward` into `color`, channel by channel.
constexpr std::uint32_t blend(std::uint32_t color, std::uint32_t toward, std::uint32_t percent) {
    std::uint32_t mixed = 0;
    for (std::uint32_t shift = 0; shift <= 16; shift += 8) {
        const std::uint32_t from = (color >> shift) & 0xFF;
        const std::uint32_t to = (toward >> shift) & 0xFF;
        mixed |= ((from * (100 - percent) + to * percent) / 100) << shift;
    }
    return mixed;
}

/// How a card of one status looks.
struct StatusStyle {
    const char* label;
    std::uint32_t background;
    std::uint32_t alertBackground;  ///< the background in the dark blink phase
    std::uint32_t text;
    bool dimLogo;
};

/// In core::AgentStatus order.
inline constexpr StatusStyle kStatusStyles[] = {
    {"WORKING", color::kWorking, color::kWorking, color::kOnDark, false},
    {"WAITING", color::kWaiting, color::kWaiting, color::kOnLight, false},
    {"ERROR", color::kError, color::kErrorDark, color::kOnDark, false},
    {"IDLE", color::kIdle, color::kIdle, color::kMutedText, true},
};
static_assert(sizeof kStatusStyles / sizeof kStatusStyles[0] == static_cast<std::size_t>(core::AgentStatus::Idle) + 1,
              "one style per status");

/// The widest status label in every font: cards of the same size all draw
/// their label in the largest font this one fits in.
constexpr const char* kWidestLabel = "WORKING";

inline const StatusStyle& styleFor(core::AgentStatus status) {
    return kStatusStyles[static_cast<std::size_t>(status)];
}

}  // namespace managents::ui
