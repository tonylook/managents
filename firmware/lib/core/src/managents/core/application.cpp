#include "managents/core/application.hpp"

namespace managents::core {

Application::Application(Display& display, StatusIndicator& indicator, HostLink& link, const DeviceInfo& device,
                         const ScreenGeometry& geometry, Pager pager)
    : display_(display), indicator_(indicator), link_(link), device_(device), geometry_(geometry), pager_(pager) {}

void Application::begin(std::uint32_t nowMs) {
    sendHello();
    tick(nowMs);
}

void Application::onReceive(const char* data, std::size_t length, std::uint32_t nowMs) {
    for (std::size_t i = 0; i < length; ++i) {
        if (assembler_.push(data[i])) {
            handleLine(assembler_.line(), assembler_.length(), nowMs);
        }
    }
}

void Application::onTouch(bool pressed, std::uint32_t nowMs) {
    if (tap_.update(pressed, nowMs) && hostConnected(nowMs)) {
        pager_.next(host_.agentCount, nowMs);
    }
}

void Application::tick(std::uint32_t nowMs) {
    const bool connected = hostConnected(nowMs);
    const bool blinkOn = (nowMs / kBlinkHalfPeriodMs) % 2 == 0;
    pager_.update(connected ? host_.agentCount : 0, nowMs);

    SceneInput input;
    input.host = connected ? &host_ : nullptr;
    input.msSinceFrame = nowMs - lastFrameMs_;
    input.blinkOn = blinkOn;
    input.page = pager_.page();
    const Scene scene = buildScene(input, geometry_);
    if (!sceneShown_ || scene != shownScene_) {
        display_.present(scene);
        shownScene_ = scene;
        sceneShown_ = true;
    }

    indicator_.show(connected ? summarize(host_) : Attention::Disconnected, blinkOn);
}

bool Application::hostConnected(std::uint32_t nowMs) const {
    return hasFrame_ && nowMs - lastFrameMs_ < kLinkTimeoutMs;
}

void Application::handleLine(const char* line, std::size_t length, std::uint32_t nowMs) {
    switch (decodeHostMessage(line, length, host_)) {
        case MessageType::State:
            hasFrame_ = true;
            lastFrameMs_ = nowMs;
            break;
        case MessageType::Hello:
            sendHello();
            break;
        case MessageType::Invalid:
        case MessageType::Ignored:
            break;
    }
}

void Application::sendHello() {
    char line[192];
    if (encodeHello(device_, line, sizeof line) > 0) {
        link_.sendLine(line);
    }
}

}  // namespace managents::core
