#include "ui/logos.hpp"

#include <algorithm>
#include <cmath>

#include "ui/codex_logo.hpp"
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

/// The Z.ai mark: its Z, three strokes with every cut parallel to the
/// diagonal, laid out on a 16 x 16 grid that the mark, a little wider than
/// tall, fills to 78% and sits centred in (like the Codex blob's three
/// quarters). The top bar ends short of the diagonal's top and the bottom bar
/// starts after its foot: the two gaps of the original.
void drawZCode(lgfx::LovyanGFX& canvas, std::int32_t x, std::int32_t y, std::int32_t size, std::uint32_t ink) {
    const float unit = static_cast<float>(size) / 16.0F;
    const auto gx = [&](float g) { return x + (1.76F + g * 0.78F) * unit; };
    const auto gy = [&](float g) { return y + (2.93F + g * 0.78F) * unit; };
    // Strokes as parallelograms, each drawn as two triangles, in grid points:
    //   diagonal   (10.4,0) (16,0) (5.6,13) (0,13)
    //   top bar    (2.8,0) (8.4,0) (6.8,2) (1.2,2)
    //   bottom bar (9,11) (14.6,11) (13,13) (7.4,13)
    auto quad = [&](float ax, float ay, float bx, float by, float cx, float cy, float dx, float dy) {
        canvas.fillTriangle(gx(ax), gy(ay), gx(bx), gy(by), gx(cx), gy(cy), ink);
        canvas.fillTriangle(gx(ax), gy(ay), gx(cx), gy(cy), gx(dx), gy(dy), ink);
    };
    quad(10.4F, 0, 16, 0, 5.6F, 13, 0, 13);  // the diagonal, corner to corner
    quad(2.8F, 0, 8.4F, 0, 6.8F, 2, 1.2F, 2);
    quad(9.0F, 11, 14.6F, 11, 13.0F, 13, 7.4F, 13);
}

/// The Codex mark: its blob in the blue-violet gradient with the `>_` prompt
/// in white, as on its colour icon. The coverage maps of codex_logo.hpp are
/// box-filtered down to the logo size; the blob spans three quarters of the
/// tile.
void drawCodex(lgfx::LovyanGFX& canvas, std::int32_t x, std::int32_t y, std::int32_t size, std::uint32_t tile,
               const std::uint32_t (&gradient)[3], std::uint32_t prompt) {
    const std::int32_t mark = std::max<std::int32_t>(size * 3 / 4, 1);
    const std::int32_t left = x + (size - mark) / 2;
    const std::int32_t top = y + (size - mark) / 2;
    for (std::int32_t row = 0; row < mark; ++row) {
        const std::int32_t r0 = row * kCodexMapSize / mark;
        const std::int32_t r1 = std::max((row + 1) * kCodexMapSize / mark, r0 + 1);
        const auto along = static_cast<std::uint32_t>(row * 200 / std::max<std::int32_t>(mark - 1, 1));
        const std::uint32_t fill =
            along <= 100 ? blend(gradient[0], gradient[1], along) : blend(gradient[1], gradient[2], along - 100);
        for (std::int32_t column = 0; column < mark; ++column) {
            const std::int32_t c0 = column * kCodexMapSize / mark;
            const std::int32_t c1 = std::max((column + 1) * kCodexMapSize / mark, c0 + 1);
            std::uint32_t blob = 0;
            std::uint32_t cut = 0;
            for (std::int32_t r = r0; r < r1; ++r) {
                for (std::int32_t c = c0; c < c1; ++c) {
                    blob += kCodexBlob[r * kCodexMapSize + c];
                    cut += kCodexPrompt[r * kCodexMapSize + c];
                }
            }
            const auto cells = static_cast<std::uint32_t>((r1 - r0) * (c1 - c0));
            if (blob == 0) {
                continue;
            }
            std::uint32_t ink = blend(tile, fill, blob * 100 / (255 * cells));
            ink = blend(ink, prompt, cut * 100 / (255 * cells));
            canvas.drawPixel(left + column, top + row, ink);
        }
    }
}

/// Positions a mark's parts on a 16-unit grid over the tile, the way the
/// OpenCode frame and the Z.ai mark are laid out: `at()` rounds a grid
/// coordinate to whole pixels, so a glyph keeps its shape at every card size.
struct Mark {
    std::int32_t x, y, size;

    std::int32_t at(float g) const { return static_cast<std::int32_t>(g * size / 16.0F + 0.5F); }
    std::int32_t px(float g) const { return x + at(g); }
    std::int32_t py(float g) const { return y + at(g); }
    float span(float g) const { return g * size / 16.0F; }
};

/// The qwen mark: a ring with a tail — a stylised Q for Qwen Code, not its
/// trademarked mark. The ring's middle is the tile colour, drawn like the
/// OpenCode frame's hole.
void drawQwen(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t ink, std::uint32_t tile) {
    const float cx = m.px(7.5F);
    const float cy = m.py(7.4F);
    canvas.fillCircle(cx, cy, m.at(6.0F), ink);
    canvas.fillCircle(cx, cy, m.at(3.8F), tile);
    canvas.drawWedgeLine(cx + m.span(3.8F) * 0.45F, cy + m.span(3.8F) * 0.45F, m.px(13.2F), m.py(13.4F), m.span(1.6F),
                         m.span(0.8F), ink);
}

/// The Gemini mark: a four-point spark with a blue gradient, top to bottom.
void drawGemini(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t top, std::uint32_t bottom) {
    const float cx = m.x + m.size / 2.0F;
    const float cy = m.y + m.size / 2.0F;
    constexpr float kQuarter = 1.5707963F;
    const float length = m.span(6.8F);
    const float base = m.span(1.5F);
    struct Ray {
        float angle;
        std::uint32_t ink;
    };
    const std::uint32_t middle = blend(top, bottom, 50);
    const Ray rays[] = {{-kQuarter, top}, {0.0F, middle}, {kQuarter, bottom}, {2 * kQuarter, middle}};
    for (const Ray& ray : rays) {
        canvas.drawWedgeLine(cx, cy, cx + std::cos(ray.angle) * length, cy + std::sin(ray.angle) * length, base,
                             base * 0.15F, ray.ink);
    }
}

/// The pi mark: a lowercase pi, its bar overhanging the left leg. Drawn inside
/// its own 16-unit box, so omp can shrink it.
void drawPiMark(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t ink) {
    auto rect = [&](float l, float t, float r, float b) {
        canvas.fillRect(m.px(l), m.py(t), m.at(r - l), m.at(b - t), ink);
    };
    rect(1.2F, 2.6F, 14.8F, 5.0F);    // the bar, past both legs
    rect(3.0F, 5.0F, 5.0F, 13.8F);    // the left leg
    rect(10.6F, 5.0F, 12.6F, 13.8F);  // the right leg
}

/// The droid mark: a robot head — antenna dot, rounded head, two eyes cut out.
void drawDroid(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t ink, std::uint32_t tile) {
    const float cx = m.x + m.size / 2.0F;
    canvas.drawWedgeLine(cx, m.py(3.4F), cx, m.py(5.6F), m.span(0.7F), m.span(0.7F), ink);
    canvas.fillCircle(cx, m.py(1.6F), m.at(0.8F), ink);
    canvas.fillRoundRect(m.px(2.6F), m.py(4.8F), m.at(10.8F), m.at(9.0F), m.at(2.4F), ink);
    canvas.fillCircle(m.px(5.9F), m.py(9.0F), m.at(1.3F), tile);
    canvas.fillCircle(m.px(10.1F), m.py(9.0F), m.at(1.3F), tile);
}

/// The Crush mark: a heart, for Charmbracelet's Crush.
void drawCrush(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t ink) {
    const float cx = m.x + m.size / 2.0F;
    const float cy = m.py(6.4F);
    const float r = m.span(3.0F);
    canvas.fillCircle(cx - r * 0.95F, cy, r, ink);
    canvas.fillCircle(cx + r * 0.95F, cy, r, ink);
    canvas.fillTriangle(cx - r * 1.8F, cy + r * 0.4F, cx + r * 1.8F, cy + r * 0.4F, cx, m.py(14.2F), ink);
}

/// The goose mark: a white bird — round body, curled neck, an orange beak.
void drawGoose(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t body, std::uint32_t beak, std::uint32_t tile) {
    canvas.fillCircle(m.px(9.8F), m.py(10.6F), m.at(4.5F), body);
    canvas.fillCircle(m.px(7.0F), m.py(9.4F), m.at(3.4F), body);
    canvas.drawWedgeLine(m.px(6.2F), m.py(9.8F), m.px(5.4F), m.py(5.2F), m.span(1.5F), m.span(1.2F), body);
    canvas.fillCircle(m.px(5.7F), m.py(3.8F), m.at(1.8F), body);
    canvas.fillTriangle(m.px(4.0F), m.py(2.6F), m.px(1.2F), m.py(3.7F), m.px(4.0F), m.py(4.8F), beak);
    canvas.fillCircle(m.px(6.2F), m.py(3.4F), m.at(0.5F), tile);
}

/// The Amp mark: a bolt, its hexagon fanned into four triangles.
void drawAmp(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t ink) {
    const std::int32_t px[6] = {m.px(9.8F), m.px(3.6F), m.px(8.0F), m.px(6.2F), m.px(12.4F), m.px(7.6F)};
    const std::int32_t py[6] = {m.py(1.2F), m.py(9.0F), m.py(9.0F), m.py(14.8F), m.py(6.6F), m.py(6.6F)};
    for (int i = 0; i < 4; ++i) {
        canvas.fillTriangle(px[5], py[5], px[i], py[i], px[i + 1], py[i + 1], ink);
    }
}

/// The omp mark: pi's glyph, small and low, with a spark — for Oh My Pi.
void drawOmp(lgfx::LovyanGFX& canvas, const Mark& m, std::uint32_t ink) {
    const std::int32_t mark = m.size * 12 / 16;
    drawPiMark(canvas, {m.x, m.y + (m.size - mark) * 3 / 4, mark}, ink);
    const float cx = m.x + m.size * 12.8F / 16.0F;
    const float cy = m.y + m.size * 2.6F / 16.0F;
    const float length = m.span(2.2F);
    const float base = m.span(0.5F);
    for (const float angle : {0.0F, 1.5707963F, 3.14159265F, 4.7123890F}) {
        canvas.drawWedgeLine(cx, cy, cx + std::cos(angle) * length, cy + std::sin(angle) * length, base, base * 0.2F,
                             ink);
    }
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
        case core::AgentKind::Codex:
            drawCodex(canvas, x, y, size, tile,
                      {ink(color::kCodexTop), ink(color::kCodexMiddle), ink(color::kCodexBottom)},
                      ink(color::kCodexPrompt));
            break;
        case core::AgentKind::ZCode:
            drawZCode(canvas, x, y, size, ink(color::kZCode));
            break;
        case core::AgentKind::Qwen:
            drawQwen(canvas, {x, y, size}, ink(color::kQwen), tile);
            break;
        case core::AgentKind::Gemini:
            drawGemini(canvas, {x, y, size}, ink(color::kGeminiTop), ink(color::kGeminiBottom));
            break;
        case core::AgentKind::Pi:
            drawPiMark(canvas, {x, y, size}, ink(color::kPi));
            break;
        case core::AgentKind::Droid:
            drawDroid(canvas, {x, y, size}, ink(color::kDroid), tile);
            break;
        case core::AgentKind::Crush:
            drawCrush(canvas, {x, y, size}, ink(color::kCrush));
            break;
        case core::AgentKind::Goose:
            drawGoose(canvas, {x, y, size}, ink(color::kGoose), ink(color::kGooseBeak), tile);
            break;
        case core::AgentKind::Amp:
            drawAmp(canvas, {x, y, size}, ink(color::kAmp));
            break;
        case core::AgentKind::Omp:
            drawOmp(canvas, {x, y, size}, ink(color::kOmp));
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
