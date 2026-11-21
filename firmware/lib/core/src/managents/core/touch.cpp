#include "managents/core/touch.hpp"

#include <cmath>

namespace managents::core {
namespace {

/// The axis of the mapping: the screen coordinate of a raw reading between two
/// reference touches. `rawA/rawB` are the reference readings along this axis,
/// `screenA/screenB` the screen coordinates they stand for.
std::int16_t mapAxis(std::int32_t raw, std::int32_t rawA, std::int32_t rawB, std::int32_t screenA,
                     std::int32_t screenB) {
    if (rawA == rawB) {
        return static_cast<std::int16_t>(screenA);
    }
    const double screen = screenA + static_cast<double>(screenB - screenA) * (raw - rawA) / (rawB - rawA);
    return static_cast<std::int16_t>(std::lround(screen));
}

void putI16(std::uint8_t* out, std::int16_t value) {
    out[0] = static_cast<std::uint8_t>(value & 0xFF);
    out[1] = static_cast<std::uint8_t>((value >> 8) & 0xFF);
}

std::int16_t getI16(const std::uint8_t* in) {
    return static_cast<std::int16_t>(static_cast<std::uint16_t>(in[0]) | (static_cast<std::uint16_t>(in[1]) << 8));
}

TouchPoint getPoint(const std::uint8_t* in) {
    TouchPoint point;
    point.x = getI16(in);
    point.y = getI16(in + 2);
    return point;
}

}  // namespace

TouchInput::Event TouchInput::update(bool pressed, TouchPoint position, std::uint32_t nowMs) {
    if (pressed) {
        switch (state_) {
            case State::Held: {
                const std::uint32_t heldMs = nowMs - pressMs_;
                if (!longFired_ && heldMs >= kLongPressMs) {
                    longFired_ = true;
                    return Event::LongPress;
                }
                if (!holdFired_ && heldMs >= kRecalibrateHoldMs) {
                    holdFired_ = true;
                    return Event::Hold;
                }
                return Event::None;
            }
            case State::Debounce:
                // The window is measured from the press start, as TapDetector's
                // was: a press after a long one is accepted at once, a bounce
                // right after a short one is not.
                if (nowMs - pressMs_ < kReleaseMs) {
                    return Event::None;
                }
                break;
            case State::Idle:
                break;
        }
        state_ = State::Held;
        longFired_ = false;
        holdFired_ = false;
        pressMs_ = nowMs;
        position_ = position;
        return Event::Press;
    }

    switch (state_) {
        case State::Held:
            state_ = State::Debounce;
            if (!longFired_) {
                return Event::Tap;  // released inside the threshold: the plain tap
            }
            return Event::None;  // the long press already fired; its release is nothing
        case State::Debounce:
            if (nowMs - pressMs_ >= kReleaseMs) {
                state_ = State::Idle;
            }
            return Event::None;
        case State::Idle:
            break;
    }
    return Event::None;
}

namespace {

/// Keeps a mapped position on the screen: raw readings can be anywhere.
TouchPoint clampToScreen(TouchPoint point, std::int16_t width, std::int16_t height) {
    if (point.x < 0) {
        point.x = 0;
    }
    if (point.y < 0) {
        point.y = 0;
    }
    if (point.x > width - 1) {
        point.x = width - 1;
    }
    if (point.y > height - 1) {
        point.y = height - 1;
    }
    return point;
}

}  // namespace

TouchPoint TouchCalibration::map(TouchPoint raw, std::int16_t width, std::int16_t height) const {
    if (!valid) {
        // The panel's nominal full range (the XPT2046 is a 12-bit converter).
        const TouchPoint mapped{
            static_cast<std::int16_t>(static_cast<std::int32_t>(raw.x) * (width - 1) / 4095),
            static_cast<std::int16_t>(static_cast<std::int32_t>(raw.y) * (height - 1) / 4095),
        };
        return clampToScreen(mapped, width, height);
    }
    // Which raw axis follows which screen axis is read off the two reference
    // touches: the axis that moved the way the targets moved along x is the
    // one to use for x. The targets are deliberately off-square, so the
    // answer is unambiguous whatever the panel's orientation or mirror.
    const std::int32_t rawDx = std::abs(rawB.x - rawA.x);
    const std::int32_t rawDy = std::abs(rawB.y - rawA.y);
    const std::int32_t screenDx = std::abs(screenB.x - screenA.x);
    const std::int32_t screenDy = std::abs(screenB.y - screenA.y);
    const bool xFollowsX = (rawDx >= rawDy) == (screenDx >= screenDy);
    const TouchPoint mapped{
        xFollowsX ? mapAxis(raw.x, rawA.x, rawB.x, screenA.x, screenB.x)
                  : mapAxis(raw.x, rawA.y, rawB.y, screenA.x, screenB.x),
        xFollowsX ? mapAxis(raw.y, rawA.y, rawB.y, screenA.y, screenB.y)
                  : mapAxis(raw.y, rawA.x, rawB.x, screenA.y, screenB.y),
    };
    return clampToScreen(mapped, width, height);
}

bool calibrationIsSound(const TouchCalibration& calibration) {
    // Far enough apart, along both paired axes, for the scales to be trusted:
    // two touches on the same spot cannot give one.
    const std::int32_t rawDx = std::abs(calibration.rawB.x - calibration.rawA.x);
    const std::int32_t rawDy = std::abs(calibration.rawB.y - calibration.rawA.y);
    const std::int32_t screenDx = std::abs(calibration.screenB.x - calibration.screenA.x);
    const std::int32_t screenDy = std::abs(calibration.screenB.y - calibration.screenA.y);
    const bool xFollowsX = (rawDx >= rawDy) == (screenDx >= screenDy);
    const bool xSound =
        (xFollowsX ? rawDx : rawDy) >= kMinRawDelta && (xFollowsX ? screenDx : screenDy) >= kMinScreenDelta;
    const bool ySound =
        (xFollowsX ? rawDy : rawDx) >= kMinRawDelta && (xFollowsX ? screenDy : screenDx) >= kMinScreenDelta;
    return xSound && ySound;
}

std::size_t encodeCalibration(const TouchCalibration& calibration, std::int16_t width, std::int16_t height,
                              std::uint8_t* out, std::size_t capacity) {
    if (capacity < kCalibrationBlobSize) {
        return 0;
    }
    out[0] = 'M';
    out[1] = 'C';
    out[2] = 1;  // the layout's version
    out[3] = 0;  // reserved
    putI16(out + 4, width);
    putI16(out + 6, height);
    const TouchPoint points[4] = {calibration.rawA, calibration.rawB, calibration.screenA, calibration.screenB};
    for (std::size_t i = 0; i < 4; ++i) {
        putI16(out + 8 + i * 4, points[i].x);
        putI16(out + 8 + i * 4 + 2, points[i].y);
    }
    return kCalibrationBlobSize;
}

bool decodeCalibration(const std::uint8_t* data, std::size_t length, std::int16_t width, std::int16_t height,
                       TouchCalibration& out) {
    if (length != kCalibrationBlobSize || data[0] != 'M' || data[1] != 'C' || data[2] != 1) {
        return false;
    }
    if (getI16(data + 4) != width || getI16(data + 6) != height) {
        return false;  // laid out for another screen: recalibrate
    }
    TouchCalibration calibration;
    calibration.rawA = getPoint(data + 8);
    calibration.rawB = getPoint(data + 12);
    calibration.screenA = getPoint(data + 16);
    calibration.screenB = getPoint(data + 20);
    if (!calibrationIsSound(calibration)) {
        return false;
    }
    calibration.valid = true;
    out = calibration;
    return true;
}

}  // namespace managents::core
