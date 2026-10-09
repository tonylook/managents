#include "adapters/lgfx_display.hpp"

namespace managents::adapters {

LgfxDisplay::LgfxDisplay(lgfx::LovyanGFX& target, const ui::LinkScreenText& text)
    : target_(target), strip_(&target), text_(text) {}

void LgfxDisplay::begin() {
    strip_.setColorDepth(16);
    buffered_ = strip_.createSprite(target_.width(), kStripHeight) != nullptr;
}

void LgfxDisplay::present(const core::Scene& scene) {
    const std::int32_t width = target_.width();
    const std::int32_t height = target_.height();

    if (!buffered_) {
        ui::ScenePainter(target_, 0, width, height, text_).paint(scene);
        return;
    }

    target_.startWrite();
    for (std::int32_t top = 0; top < height; top += kStripHeight) {
        ui::ScenePainter(strip_, top, width, height, text_).paint(scene);
        strip_.pushSprite(0, top);
        target_.waitDMA();  // the strip buffer is reused for the next band
    }
    target_.endWrite();
}

}  // namespace managents::adapters
