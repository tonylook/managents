#pragma once

#include <cstddef>
#include <cstdint>

#include "managents/core/line_assembler.hpp"
#include "managents/core/model.hpp"
#include "managents/core/pager.hpp"
#include "managents/core/ports.hpp"
#include "managents/core/protocol.hpp"
#include "managents/core/scene.hpp"

namespace managents::core {

/// The firmware's use case: turn the host's byte stream into what the screen
/// and the LED show. Owns no hardware; time is passed in explicitly.
class Application {
public:
    /// The host is considered gone after this much silence (docs/protocol.md).
    static constexpr std::uint32_t kLinkTimeoutMs = 6000;
    /// Error cards and the LED blink with this half-period.
    static constexpr std::uint32_t kBlinkHalfPeriodMs = 500;

    Application(Display& display, StatusIndicator& indicator, HostLink& link, const DeviceInfo& device,
                const ScreenGeometry& geometry, Pager pager = Pager{});

    /// Announces the device and draws the first screen.
    void begin(std::uint32_t nowMs);

    /// Feeds bytes received from the host.
    void onReceive(const char* data, std::size_t length, std::uint32_t nowMs);

    /// Feeds the touch panel state; a tap anywhere shows the next page.
    void onTouch(bool pressed, std::uint32_t nowMs);

    /// Advances timers and refreshes the outputs if anything visible changed.
    void tick(std::uint32_t nowMs);

    bool hostConnected(std::uint32_t nowMs) const;

private:
    void handleLine(const char* line, std::size_t length, std::uint32_t nowMs);
    void sendHello();

    Display& display_;
    StatusIndicator& indicator_;
    HostLink& link_;
    DeviceInfo device_;
    ScreenGeometry geometry_;

    LineAssembler assembler_;
    Pager pager_;
    TapDetector tap_;
    HostState host_;
    bool hasFrame_ = false;
    std::uint32_t lastFrameMs_ = 0;

    Scene shownScene_;
    bool sceneShown_ = false;
};

}  // namespace managents::core
