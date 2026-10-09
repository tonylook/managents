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
/// and the LED show. Owns no hardware; time is passed in explicitly, and
/// tick() must run more often than every 49 days (the millis() wrap).
class Application {
public:
    /// The host is considered gone after this much silence (docs/protocol.md).
    static constexpr std::uint32_t kLinkTimeoutMs = 6000;
    /// Without a word from the helper for this long, the screen shows how to
    /// set it up. Well above what a starting helper needs to find the display
    /// and say hello.
    static constexpr std::uint32_t kSetupHintAfterMs = 15000;
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
    LinkState linkState() const;

    Display& display_;
    StatusIndicator& indicator_;
    HostLink& link_;
    DeviceInfo device_;
    ScreenGeometry geometry_;

    LineAssembler assembler_;
    Pager pager_;
    TapDetector tap_;
    HostState host_;

    // The link. The timeouts latch, so that a millis() wrap can neither bring
    // back a stale frame nor end the setup hint.
    bool hasFrame_ = false;   ///< a frame has ever arrived: silence now means the link was lost
    bool linkFresh_ = false;  ///< the last frame has not timed out yet
    std::uint32_t lastFrameMs_ = 0;
    bool setupHintDue_ = false;  ///< kSetupHintAfterMs passed without a frame or a hello
    std::uint32_t lastContactMs_ = 0;

    Scene shownScene_;
    bool sceneShown_ = false;
};

}  // namespace managents::core
