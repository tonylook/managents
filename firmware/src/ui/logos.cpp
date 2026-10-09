#include "ui/logos.hpp"

#include <cmath>

#include "ui/theme.hpp"

namespace managents::ui {
namespace {

constexpr float kDimAmount = 0.55F;

std::uint32_t blend(std::uint32_t color, std::uint32_t toward, float amount) {
    auto channel = [&](int shift) {
        const float from = static_cast<float>((color >> shift) & 0xFF);
        const float to = static_cast<float>((toward >> shift) & 0xFF);
        return static_cast<std::uint32_t>(from + (to - from) * amount) << shift;
    };
    return channel(16) | channel(8) | channel(0);
}

/// The Claude mark: a warm starburst of tapered rays.
void drawClaude(lgfx::LovyanGFX& canvas, float cx, float cy, float size, std::uint32_t ink) {
    constexpr int kRays = 12;
    constexpr float kPi = 3.14159265F;
    const float baseRadius = size * 0.07F;
    const float tipRadius = size * 0.035F;
    for (int i = 0; i < kRays; ++i) {
        const float angle = 2.0F * kPi * static_cast<float>(i) / kRays + 0.13F;
        const float length = size * (i % 2 == 0 ? 0.36F : 0.29F);
        const float tipX = cx + std::cos(angle) * length;
        const float tipY = cy + std::sin(angle) * length;
        canvas.drawWedgeLine(cx, cy, tipX, tipY, baseRadius, tipRadius, ink);
    }
}

/// The OpenCode mark: a white frame around a grey block (from its SVG logo).
void drawOpenCode(lgfx::LovyanGFX& canvas, float x, float y, float size, std::uint32_t outer, std::uint32_t inner,
                  std::uint32_t tile) {
    auto rect = [&](float left, float top, float right, float bottom, std::uint32_t ink) {
        canvas.fillRect(static_cast<std::int32_t>(x + left * size), static_cast<std::int32_t>(y + top * size),
                        static_cast<std::int32_t>((right - left) * size),
                        static_cast<std::int32_t>((bottom - top) * size), ink);
    };
    rect(0.25F, 0.1875F, 0.75F, 0.8125F, outer);
    rect(0.375F, 0.3125F, 0.625F, 0.6875F, tile);
    rect(0.375F, 0.4375F, 0.625F, 0.6875F, inner);
}

}  // namespace

void drawLogo(lgfx::LovyanGFX& canvas, core::AgentKind kind, std::int32_t x, std::int32_t y, std::int32_t size,
              bool dimmed, std::uint32_t background) {
    auto ink = [&](std::uint32_t color) { return dimmed ? blend(color, background, kDimAmount) : color; };
    const std::uint32_t tile = ink(color::kLogoTile);
    canvas.fillRoundRect(x, y, size, size, size * 22 / 100, tile);

    const float side = static_cast<float>(size);
    switch (kind) {
        case core::AgentKind::Claude:
            drawClaude(canvas, x + side / 2, y + side / 2, side, ink(color::kClaude));
            break;
        case core::AgentKind::OpenCode:
            drawOpenCode(canvas, x, y, side, ink(color::kOpenCodeOuter), ink(color::kOpenCodeInner), tile);
            break;
        case core::AgentKind::Unknown:
            canvas.fillCircle(x + size / 2, y + size / 2, size / 4, ink(color::kMutedText));
            break;
    }
}

}  // namespace managents::ui
