#pragma once

#include <cstdint>

#define LGFX_USE_V1
#include <LovyanGFX.hpp>

#include "managents/core/scene.hpp"

namespace managents::ui {

/// Paints a Scene onto any LovyanGFX canvas. The canvas may be a horizontal
/// strip of the screen starting at `originY`: everything is drawn shifted by
/// that amount and clipped by the canvas, so the same code renders the whole
/// screen strip by strip without a full-frame buffer.
class ScenePainter {
public:
    ScenePainter(lgfx::LovyanGFX& canvas, std::int32_t originY, std::int32_t screenWidth, std::int32_t screenHeight,
                 const char* projectUrl);

    void paint(const core::Scene& scene);

private:
    void paintHeader(const core::HeaderView& header);
    void paintPageDots(const core::HeaderView& header, std::int32_t middle);
    void paintCard(const core::CardView& card);
    void paintContextBar(const core::CardView& card, std::int32_t left, std::int32_t top, std::int32_t width);
    void paintNoAgents();
    void paintWaitingForHost();

    bool intersects(std::int32_t top, std::int32_t height) const;
    std::int32_t y(std::int32_t sceneY) const { return sceneY - originY_; }

    lgfx::LovyanGFX& canvas_;
    std::int32_t originY_;
    std::int32_t width_;
    std::int32_t height_;
    const char* projectUrl_;
};

}  // namespace managents::ui
