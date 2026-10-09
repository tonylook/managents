#pragma once

#include <cstdint>

#include <LovyanGFX.hpp>

#include "managents/core/ports.hpp"

namespace managents::adapters {

/// core::Display on a LovyanGFX target: the panel on the board, a full-screen
/// sprite in the host renderer. Each scene is painted into a strip sprite one
/// band at a time and pushed to the target: no flicker, and no full-screen
/// buffer (the ESP32-WROOM-32E has no PSRAM).
class LgfxDisplay : public core::Display {
public:
    static constexpr std::int32_t kStripHeight = 40;

    LgfxDisplay(lgfx::LovyanGFX& target, const char* setupUrl);

    /// Allocates the strip buffer. Without it, scenes are drawn straight to the target.
    void begin();

    void present(const core::Scene& scene) override;

private:
    lgfx::LovyanGFX& target_;
    lgfx::LGFX_Sprite strip_;
    const char* setupUrl_;
    bool buffered_ = false;
};

}  // namespace managents::adapters
