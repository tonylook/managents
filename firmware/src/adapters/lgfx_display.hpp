#pragma once

#include <cstdint>

#define LGFX_USE_V1
#include <LovyanGFX.hpp>

#include "managents/core/ports.hpp"

namespace managents::adapters {

/// core::Display on a LovyanGFX panel. Each scene is painted into a strip
/// sprite one band at a time and pushed to the panel: no flicker, and no
/// full-screen buffer (the ESP32-WROOM-32E has no PSRAM).
class LgfxDisplay : public core::Display {
public:
    static constexpr std::int32_t kStripHeight = 40;

    LgfxDisplay(lgfx::LGFX_Device& panel, const char* projectUrl);

    /// Allocates the strip buffer. Without it, scenes are drawn straight to the panel.
    void begin();

    void present(const core::Scene& scene) override;

private:
    lgfx::LGFX_Device& panel_;
    lgfx::LGFX_Sprite strip_;
    const char* projectUrl_;
    bool buffered_ = false;
};

}  // namespace managents::adapters
