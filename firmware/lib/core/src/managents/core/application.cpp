#include "managents/core/application.hpp"

#include "managents/core/chime.hpp"

namespace managents::core {
namespace {

/// True if an agent shows an error young enough to blink.
bool anyBlinks(const HostState& host, std::uint32_t elapsedSeconds) {
    for (std::uint8_t i = 0; i < host.agentCount; ++i) {
        if (blinks(host.agents[i], elapsedSeconds)) {
            return true;
        }
    }
    return false;
}

}  // namespace

bool dimsBacklight(SceneKind kind, Attention attention, std::uint32_t msUnchanged) {
    switch (kind) {
        case SceneKind::Reconnecting:
            return msUnchanged >= kDimWhileAsleepAfterMs;
        case SceneKind::NoAgents:
        case SceneKind::Agents:
            return attention == Attention::Quiet && msUnchanged >= kDimWhileQuietAfterMs;
        case SceneKind::Connecting:
        case SceneKind::SetupNeeded:
        case SceneKind::Detail:       // someone opened it and is reading it
        case SceneKind::Calibration:  // someone is touching it
            break;
    }
    return false;
}

Application::Application(Display& display, StatusIndicator& indicator, HostLink& link, const DeviceInfo& device,
                         const ScreenGeometry& geometry, Pager pager, SettingsStore* store, SoundOutput* sound)
    : display_(display),
      indicator_(indicator),
      link_(link),
      device_(device),
      geometry_(geometry),
      pager_(pager),
      store_(store),
      sound_(sound) {}

void Application::begin(std::uint32_t nowMs) {
    if (store_ != nullptr) {
        soundEnabled_ = store_->getBool(kSoundSettingKey, kSoundByDefault);
        loadCalibration();
    }
    lastContactMs_ = nowMs;  // the setup hint counts from boot
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

void Application::onTouch(bool pressed, const TouchPoint& raw, std::uint32_t nowMs) {
    const TouchInput::Event event = touch_.update(pressed, raw, nowMs);
    switch (event) {
        case TouchInput::Event::None:
            return;
        case TouchInput::Event::Press:
            restingSinceMs_ = nowMs;
            // A press on a dimmed screen only wakes it, whatever it becomes:
            // nobody watched it land, so it opens nothing.
            pressSwallowed_ = dimmed_;
            if (pressSwallowed_) {
                dimmed_ = false;
            }
            return;
        case TouchInput::Event::Tap:
        case TouchInput::Event::LongPress:
        case TouchInput::Event::Hold:
            break;
    }

    if (pressSwallowed_) {
        pressSwallowed_ = false;  // the press that woke the screen does nothing else
        return;
    }
    if (calibrationStep_ >= 0) {
        // The calibration owns the touches: a tap is a sample, a long press
        // leaves it. The hold of the same press is nothing — it already left.
        if (event == TouchInput::Event::Tap) {
            takeCalibrationSample(touch_.position());
        } else if (event == TouchInput::Event::LongPress) {
            cancelCalibration();
            pressSwallowed_ = true;  // this press is done: its hold must not re-open the flow
        }
        return;
    }
    if (!detailId_.empty()) {
        // A tap or a long press goes back to the grid; holding on recalibrates.
        if (event == TouchInput::Event::Hold) {
            beginCalibration();
        } else {
            closeDetail();
        }
        return;
    }
    switch (event) {
        case TouchInput::Event::Tap:
            if (hostConnected(nowMs)) {
                pager_.next(host_.agentCount, nowMs);
            }
            break;
        case TouchInput::Event::LongPress:
            openLongPress(nowMs);
            break;
        case TouchInput::Event::Hold:
            beginCalibration();  // a hold-and-a-half anywhere: (re)run the calibration
            break;
        default:
            break;
    }
}

void Application::tick(std::uint32_t nowMs) {
    linkFresh_ = hostConnected(nowMs);
    setupHintDue_ = setupHintDue_ || nowMs - lastContactMs_ >= kSetupHintAfterMs;
    const std::uint32_t msSinceFrame = nowMs - lastFrameMs_;
    const bool blinkOn = (nowMs / kBlinkHalfPeriodMs) % 2 == 0;
    pager_.update(linkFresh_ ? host_.agentCount : 0, nowMs);

    // The detail view follows its agent by id; once the agent or the link is
    // gone, the grid returns.
    const Agent* detail = detailAgent();
    if (detail == nullptr) {
        closeDetail();
    }

    SceneInput input;
    input.host = linkFresh_ ? &host_ : nullptr;
    input.msSinceFrame = msSinceFrame;
    input.blinkOn = blinkOn;
    input.page = pager_.page();
    input.link = linkState();
    input.detail = detail;
    input.calibrationStep = calibrationStep_;
    const Scene scene = buildScene(input, geometry_);
    if (!sceneShown_ || scene != shownScene_) {
        display_.present(scene);
        shownScene_ = scene;
        sceneShown_ = true;
    }

    const Attention attention = linkFresh_ ? summarize(host_) : Attention::Disconnected;
    updateBacklight(scene.kind, attention, nowMs);
    const bool freshError = linkFresh_ && anyBlinks(host_, msSinceFrame / 1000);
    indicator_.show(attention, blinkOn || !freshError);
}

const Agent* Application::detailAgent() const {
    if (detailId_.empty() || !linkFresh_) {
        return nullptr;
    }
    for (std::uint8_t i = 0; i < host_.agentCount; ++i) {
        if (host_.agents[i].id == detailId_) {
            return &host_.agents[i];
        }
    }
    return nullptr;
}

bool Application::hostConnected(std::uint32_t nowMs) const {
    return linkFresh_ && nowMs - lastFrameMs_ < kLinkTimeoutMs;
}

LinkState Application::linkState() const {
    if (hasFrame_) {
        return LinkState::Lost;
    }
    return setupHintDue_ ? LinkState::SetupNeeded : LinkState::Connecting;
}

void Application::updateBacklight(SceneKind kind, Attention attention, std::uint32_t nowMs) {
    if (kind != restingKind_ || attention != restingAttention_) {
        restingKind_ = kind;
        restingAttention_ = attention;
        restingSinceMs_ = nowMs;
        dimmed_ = false;
    }
    // Latched, so that it stays dim across the millis() wrap.
    dimmed_ = dimmed_ || dimsBacklight(kind, attention, nowMs - restingSinceMs_);
    display_.setBrightness(dimmed_ ? Brightness::Dimmed : Brightness::Full);
}

void Application::handleLine(const char* line, std::size_t length, std::uint32_t nowMs) {
    Settings settings;
    switch (decodeHostMessage(line, length, scratch_, settings)) {
        case MessageType::State: {
            // The chime compares the frame before it applies: the screen was
            // showing `host_`, and a card entering waiting or error now is
            // what the sound announces.
            const ChimeDecision chime = chimeFor(host_, scratch_);
            host_ = scratch_;
            hasFrame_ = true;
            linkFresh_ = true;
            lastFrameMs_ = nowMs;
            lastContactMs_ = nowMs;
            setupHintDue_ = false;
            // A dimmed screen means nobody is at the desk: the chime stays
            // quiet (ADR-0014). The screen itself still wakes, because the
            // attention changed, so the state is there when they return.
            if (chime.play && soundEnabled_ && sound_ != nullptr && !dimmed_) {
                sound_->play(chime.which);
            }
            break;
        }
        case MessageType::Hello:
            lastContactMs_ = nowMs;  // a helper probing for displays: it is there
            setupHintDue_ = false;
            sendHello();
            break;
        case MessageType::Settings:
            // A setting changes nothing about the link: it arrives on the
            // sender's own connection, which may be the only sign of life the
            // helper gives while the service is stopped.
            if (settings.hasSound) {
                setSound(settings.sound);
            }
            break;
        case MessageType::Invalid:
        case MessageType::Ignored:
            break;
    }
}

void Application::setSound(bool enabled) {
    soundEnabled_ = enabled;
    if (store_ != nullptr) {
        store_->putBool(kSoundSettingKey, enabled);
    }
}

void Application::loadCalibration() {
    std::uint8_t blob[kCalibrationBlobSize];
    const std::size_t length = store_->getBlob(kTouchCalibrationKey, blob, sizeof blob);
    TouchCalibration loaded;
    if (decodeCalibration(blob, length, geometry_.width, geometry_.height, loaded)) {
        calibration_ = loaded;
    }
}

void Application::persistCalibration() {
    if (store_ == nullptr) {
        return;
    }
    std::uint8_t blob[kCalibrationBlobSize];
    if (encodeCalibration(calibration_, geometry_.width, geometry_.height, blob, sizeof blob) > 0) {
        store_->putBlob(kTouchCalibrationKey, blob, sizeof blob);
    }
}

void Application::openLongPress(std::uint32_t nowMs) {
    if (!hostConnected(nowMs)) {
        return;
    }
    if (!calibration_.valid) {
        // The finger's position cannot be trusted yet, so no card is hit
        // tested: the long press offers the guided calibration instead
        // (ADR-0014), on any screen, and the plain tap keeps paging.
        beginCalibration();
        return;
    }
    if (shownScene_.kind != SceneKind::Agents) {
        return;  // no cards to detail: link screens, no agents
    }
    const TouchPoint point = calibration_.map(touch_.position(), geometry_.width, geometry_.height);
    const std::int8_t card = cardAt(shownScene_, point.x, point.y);
    if (card < 0) {
        return;  // the header, the dots, the +N badge, the gaps: not cards
    }
    // cardAt answers the index within the page's cards; the agent list holds
    // every agent, so the page's first card comes first.
    const std::size_t index = pager_.firstItem() + static_cast<std::size_t>(card);
    if (index >= host_.agentCount) {
        return;  // the list shrank under the page shown
    }
    detailId_.assign(host_.agents[index].id.c_str());
}

void Application::beginCalibration() {
    calibrationStep_ = 0;
}

void Application::cancelCalibration() {
    calibrationStep_ = -1;
}

void Application::takeCalibrationSample(const TouchPoint& raw) {
    const TouchPoint target = calibrationTarget(static_cast<std::size_t>(calibrationStep_), geometry_);
    if (calibrationStep_ == 0) {
        calibration_ = TouchCalibration{};
        calibration_.rawA = raw;
        calibration_.screenA = target;
        calibrationStep_ = 1;
        return;
    }
    TouchCalibration done = calibration_;
    done.rawB = raw;
    done.screenB = target;
    cancelCalibration();
    if (!calibrationIsSound(done)) {
        return;  // two touches on the same spot say nothing about the panel
    }
    done.valid = true;
    calibration_ = done;
    persistCalibration();
}

void Application::sendHello() {
    char line[192];
    if (encodeHello(device_, line, sizeof line) > 0) {
        link_.sendLine(line);
    }
}

}  // namespace managents::core
