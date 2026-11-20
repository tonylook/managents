#pragma once

#include <LovyanGFX.hpp>

#include "managents/core/touch.hpp"

namespace managents::adapters {

/// Reads the resistive panel (XPT2046) as raw sensor coordinates while it is
/// pressed. The raw readings are whatever the controller reports: the core's
/// touch calibration maps them onto the screen (ADR-0014), which is what keeps
/// this adapter free of per-unit knowledge.
class TouchSensor {
public:
    explicit TouchSensor(lgfx::LGFX_Device& panel) : panel_(panel) {}

    /// True while the panel is pressed, with the raw reading in `raw`.
    bool read(core::TouchPoint& raw) {
        lgfx::touch_point_t point;
        if (panel_.getTouchRaw(&point, 1) <= 0) {
            return false;
        }
        raw.x = point.x;
        raw.y = point.y;
        return true;
    }

private:
    lgfx::LGFX_Device& panel_;
};

}  // namespace managents::adapters
