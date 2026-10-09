#include "ui/scene_painter.hpp"

#include <algorithm>

#include "ui/logos.hpp"
#include "ui/text.hpp"
#include "ui/theme.hpp"

namespace managents::ui {
namespace {

using core::CardView;
using core::Rect;

constexpr std::int32_t kCardRadius = 10;
constexpr std::int32_t kBarHeight = 4;

/// Card metrics derived from its size, like the POC's CSS container queries.
struct CardMetrics {
    std::int32_t padding;
    std::int32_t gap;
    std::int32_t logo;
    const lgfx::IFont* ageFont;
};

CardMetrics metricsFor(const Rect& bounds) {
    const std::int32_t height = bounds.h;
    CardMetrics metrics{};
    metrics.padding = std::clamp<std::int32_t>(height / 10, 6, 14);
    metrics.gap = std::max<std::int32_t>(metrics.padding / 2, 3);
    metrics.logo = std::clamp<std::int32_t>(height * 22 / 100, 16, 40);
    metrics.ageFont = height >= 150 ? &fonts::FreeSansBold12pt7b : &fonts::FreeSans9pt7b;
    return metrics;
}

/// GFX fonts draw about three quarters of their line height above the baseline.
std::int32_t ascentOf(lgfx::LovyanGFX& canvas) {
    return canvas.fontHeight() * 3 / 4;
}

}  // namespace

ScenePainter::ScenePainter(lgfx::LovyanGFX& canvas, std::int32_t originY, std::int32_t screenWidth,
                           std::int32_t screenHeight, const char* projectUrl)
    : canvas_(canvas), originY_(originY), width_(screenWidth), height_(screenHeight), projectUrl_(projectUrl) {}

void ScenePainter::paint(const core::Scene& scene) {
    canvas_.fillScreen(color::kBackground);
    switch (scene.kind) {
        case core::SceneKind::WaitingForHost:
            paintWaitingForHost();
            return;
        case core::SceneKind::NoAgents:
            paintHeader(scene.header);
            paintNoAgents();
            return;
        case core::SceneKind::Agents:
            paintHeader(scene.header);
            for (std::uint8_t i = 0; i < scene.cardCount; ++i) {
                if (intersects(scene.cards[i].bounds.y, scene.cards[i].bounds.h)) {
                    paintCard(scene.cards[i]);
                }
            }
            return;
    }
}

bool ScenePainter::intersects(std::int32_t top, std::int32_t height) const {
    return top < originY_ + canvas_.height() && top + height > originY_;
}

void ScenePainter::paintHeader(const core::HeaderView& header) {
    const Rect& bounds = header.bounds;
    if (!intersects(bounds.y, bounds.h)) {
        return;
    }
    const std::int32_t middle = y(bounds.y + bounds.h / 2);
    canvas_.setFont(&fonts::FreeSans9pt7b);
    canvas_.setTextColor(color::kFaintText);
    canvas_.setTextDatum(textdatum_t::middle_left);
    canvas_.drawString("managents", bounds.x + 10, middle);

    canvas_.setTextColor(color::kMutedText);
    canvas_.setTextDatum(textdatum_t::middle_right);
    const std::int32_t clockRight = bounds.x + bounds.w - 10;
    canvas_.drawString(header.clock.c_str(), clockRight, middle);

    if (!header.overflowBadge.empty()) {
        const std::int32_t badgeRight = clockRight - canvas_.textWidth(header.clock.c_str()) - 12;
        canvas_.setTextColor(color::kBrightText);
        canvas_.drawString(header.overflowBadge.c_str(), badgeRight, middle);
    }
    paintPageDots(header, middle);
}

void ScenePainter::paintPageDots(const core::HeaderView& header, std::int32_t middle) {
    if (header.pageCount < 2) {
        return;
    }
    constexpr std::int32_t kRadius = 3;
    constexpr std::int32_t kSpacing = 14;
    const std::int32_t first = width_ / 2 - (header.pageCount - 1) * kSpacing / 2;
    for (std::int32_t i = 0; i < header.pageCount; ++i) {
        const std::uint32_t ink = i == header.page ? color::kBrightText : color::kFaintText;
        canvas_.fillCircle(first + i * kSpacing, middle, kRadius, ink);
    }
}

// Card layout:
//   [logo]                 age
//   name, full width, one or two lines
//   STATUS
//   ==== context bar ====
void ScenePainter::paintCard(const CardView& card) {
    const Rect& bounds = card.bounds;
    const CardPalette palette = paletteFor(card.status, card.alertPhase);
    const CardMetrics metrics = metricsFor(bounds);
    const std::int32_t top = y(bounds.y);
    const std::int32_t bottom = top + bounds.h;
    const std::int32_t left = bounds.x + metrics.padding;
    const std::int32_t right = bounds.x + bounds.w - metrics.padding;
    const std::int32_t innerWidth = right - left;

    canvas_.fillRoundRect(bounds.x, top, bounds.w, bounds.h, kCardRadius, palette.background);
    canvas_.setTextColor(palette.text);

    const std::int32_t logoTop = top + metrics.padding;
    drawLogo(canvas_, card.kind, left, logoTop, metrics.logo, palette.dimLogo, palette.background);
    canvas_.setFont(metrics.ageFont);
    canvas_.setTextDatum(textdatum_t::middle_right);
    canvas_.drawString(card.age.c_str(), right, logoTop + metrics.logo / 2);

    std::int32_t baseline = bottom - metrics.padding;
    if (card.context.visible) {
        const std::int32_t barTop = bottom - metrics.gap - kBarHeight;
        paintContextBar(card, left, barTop, innerWidth);
        baseline = barTop - metrics.gap;
    }

    // The status gets up to ~45% of the space under the logo row, the name the rest.
    const std::int32_t nameTop = logoTop + metrics.logo + metrics.gap;
    const char* label = labelFor(card.status);
    const std::int32_t labelBudget = (baseline - nameTop) * 45 / 100 * 4 / 3;
    selectLargestFitting(canvas_, kBoldFonts, kBoldFontCount, label, innerWidth, labelBudget);
    const std::int32_t labelTop = baseline - ascentOf(canvas_);
    canvas_.setTextDatum(textdatum_t::baseline_left);
    canvas_.drawString(label, left, baseline);

    const NameLayout name = layoutName(canvas_, card.name.c_str(), innerWidth, labelTop - metrics.gap - nameTop);
    canvas_.setFont(name.font);
    canvas_.setTextDatum(textdatum_t::top_left);
    for (std::size_t line = 0; line < name.lineCount; ++line) {
        canvas_.drawString(name.lines[line], left, nameTop + static_cast<std::int32_t>(line) * canvas_.fontHeight());
    }
}

void ScenePainter::paintContextBar(const CardView& card, std::int32_t left, std::int32_t top, std::int32_t width) {
    canvas_.fillRoundRect(left, top, width, kBarHeight, kBarHeight / 2, color::kBarTrack);
    const std::int32_t filled = width * card.context.percent / 100;
    if (filled > 0) {
        canvas_.fillRoundRect(left, top, filled, kBarHeight, kBarHeight / 2, contextBarColor(card.context.percent));
    }
}

void ScenePainter::paintNoAgents() {
    canvas_.setFont(&fonts::FreeSansBold18pt7b);
    canvas_.setTextColor(color::kFaintText);
    canvas_.setTextDatum(textdatum_t::middle_center);
    canvas_.drawString("No agents open", width_ / 2, y(height_ / 2));
}

void ScenePainter::paintWaitingForHost() {
    const bool landscape = width_ > height_;
    const std::int32_t qrSize = landscape ? 132 : 150;
    const std::int32_t margin = 24;

    // QR code to the project page: right column in landscape, bottom in portrait.
    const std::int32_t qrX = landscape ? width_ - margin - qrSize : (width_ - qrSize) / 2;
    const std::int32_t qrY = landscape ? (height_ - qrSize) / 2 - 10 : height_ - margin - qrSize - 24;
    if (intersects(qrY - 6, qrSize + 40)) {
        canvas_.fillRoundRect(qrX - 6, y(qrY - 6), qrSize + 12, qrSize + 12, 8, 0xFFFFFF);
        canvas_.qrcode(projectUrl_, qrX, y(qrY), qrSize);
        canvas_.setFont(&fonts::FreeSans9pt7b);
        canvas_.setTextColor(color::kMutedText);
        canvas_.setTextDatum(textdatum_t::top_center);
        canvas_.drawString("setup guide", qrX + qrSize / 2, y(qrY + qrSize + 12));
    }

    const std::int32_t textLeft = margin;
    const std::int32_t textTop = landscape ? height_ / 2 - 56 : margin + 40;
    canvas_.setTextDatum(textdatum_t::top_left);
    canvas_.setFont(&fonts::FreeSansBold18pt7b);
    canvas_.setTextColor(color::kBrightText);
    canvas_.drawString("Waiting for", textLeft, y(textTop));
    canvas_.drawString("computer...", textLeft, y(textTop + 36));
    canvas_.setFont(&fonts::FreeSans9pt7b);
    canvas_.setTextColor(color::kMutedText);
    canvas_.drawString("Connect USB and start", textLeft, y(textTop + 86));
    canvas_.drawString("the managents helper.", textLeft, y(textTop + 108));
}

}  // namespace managents::ui
