#include "ui/logos.hpp"

#include <cmath>

#include "ui/theme.hpp"

namespace managents::ui {
namespace {

/// How far a dimmed logo fades into the card, in percent.
constexpr std::uint32_t kDimPercent = 55;

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

/// The OpenCode mark: a white frame around a grey block (from its SVG logo),
/// laid out on a 16-unit grid so every edge falls on a whole pixel.
void drawOpenCode(lgfx::LovyanGFX& canvas, std::int32_t x, std::int32_t y, std::int32_t size, std::uint32_t outer,
                  std::uint32_t inner, std::uint32_t tile) {
    auto rect = [&](std::int32_t left, std::int32_t top, std::int32_t right, std::int32_t bottom, std::uint32_t ink) {
        const std::int32_t x0 = x + size * left / 16;
        const std::int32_t y0 = y + size * top / 16;
        canvas.fillRect(x0, y0, x + size * right / 16 - x0, y + size * bottom / 16 - y0, ink);
    };
    rect(4, 3, 12, 13, outer);
    rect(6, 5, 10, 11, tile);
    rect(6, 7, 10, 11, inner);
}

/// The rounded tile with the agent's mark, drawn straight onto `canvas`.
void drawTile(lgfx::LovyanGFX& canvas, core::AgentKind kind, std::int32_t x, std::int32_t y, std::int32_t size,
              bool dimmed, std::uint32_t background) {
    auto ink = [&](std::uint32_t color) { return dimmed ? blend(color, background, kDimPercent) : color; };
    const std::uint32_t tile = ink(color::kLogoTile);
    canvas.fillRoundRect(x, y, size, size, size * 22 / 100, tile);

    const float side = static_cast<float>(size);
    switch (kind) {
        case core::AgentKind::Claude:
            drawClaude(canvas, x + side / 2, y + side / 2, side, ink(color::kClaude));
            break;
        case core::AgentKind::OpenCode:
            drawOpenCode(canvas, x, y, size, ink(color::kOpenCodeOuter), ink(color::kOpenCodeInner), tile);
            break;
        case core::AgentKind::Unknown:
            canvas.fillCircle(x + size / 2, y + size / 2, size / 4, ink(color::kMutedText));
            break;
    }
}

}  // namespace

void drawLogo(lgfx::LovyanGFX& canvas, core::AgentKind kind, std::int32_t x, std::int32_t y, std::int32_t size,
              bool dimmed, std::uint32_t background) {
    // LovyanGFX's anti-aliased lines round negative coordinates differently from
    // positive ones, so a logo cut by a strip boundary would not line up. Drawn at
    // the origin of its own small sprite, it gets the same pixels in every strip.
    lgfx::LGFX_Sprite tile(&canvas);
    tile.setColorDepth(16);
    if (tile.createSprite(size, size) == nullptr) {
        drawTile(canvas, kind, x, y, size, dimmed, background);
        return;
    }
    tile.fillScreen(background);
    drawTile(tile, kind, 0, 0, size, dimmed, background);
    tile.pushSprite(x, y);
}

}  // namespace managents::ui
