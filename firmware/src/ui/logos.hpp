#pragma once

#include <cstdint>

#define LGFX_USE_V1
#include <LovyanGFX.hpp>

#include "managents/core/model.hpp"

namespace managents::ui {

/// Draws the agent's logo as a rounded tile of `size` x `size` pixels at (x, y).
/// Vector-drawn, so it stays sharp at any card size. `dimmed` blends it toward
/// `background` for idle cards.
void drawLogo(lgfx::LovyanGFX& canvas, core::AgentKind kind, std::int32_t x, std::int32_t y, std::int32_t size,
              bool dimmed, std::uint32_t background);

}  // namespace managents::ui
