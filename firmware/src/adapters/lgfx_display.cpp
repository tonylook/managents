#include "adapters/lgfx_display.hpp"

#include "ui/scene_painter.hpp"

namespace managents::adapters {

LgfxDisplay::LgfxDisplay(lgfx::LGFX_Device& panel, const char* projectUrl)
    : panel_(panel), strip_(&panel), projectUrl_(projectUrl) {}

void LgfxDisplay::begin() {
    strip_.setColorDepth(16);
    buffered_ = strip_.createSprite(panel_.width(), kStripHeight) != nullptr;
}

void LgfxDisplay::present(const core::Scene& scene) {
    const std::int32_t width = panel_.width();
    const std::int32_t height = panel_.height();

    if (!buffered_) {
        ui::ScenePainter(panel_, 0, width, height, projectUrl_).paint(scene);
        return;
    }

    panel_.startWrite();
    for (std::int32_t top = 0; top < height; top += kStripHeight) {
        ui::ScenePainter(strip_, top, width, height, projectUrl_).paint(scene);
        strip_.pushSprite(0, top);
        panel_.waitDMA();  // the strip buffer is reused for the next band
    }
    panel_.endWrite();
}

}  // namespace managents::adapters
