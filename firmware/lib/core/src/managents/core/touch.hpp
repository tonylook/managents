#pragma once

#include <cstddef>
#include <cstdint>

namespace managents::core {

/// A position on the screen, or the raw reading the panel made of one.
struct TouchPoint {
    std::int16_t x = 0;
    std::int16_t y = 0;

    bool operator==(const TouchPoint& other) const { return x == other.x && y == other.y; }
    bool operator!=(const TouchPoint& other) const { return !(*this == other); }
};

/// Turns press/release samples into touch events, remembering where the press
/// started. It takes over from TapDetector, which only knew "a press": one
/// press is now either
///   - a **tap**: released within `kLongPressMs` — the plain tap that pages;
///   - a **long press**: still held when the threshold passes. It fires at the
///     threshold, not at the release, so the view opens under the finger;
///   - a **hold**: still held at `kRecalibrateHoldMs`. It re-runs the guided
///     touch calibration, the only way back into it once calibrated.
/// Each event fires at most once per press, and a press that starts within
/// `kReleaseMs` of the last one is a bounce of the resistive panel, not a
/// press (the window is measured from the press start, as TapDetector's was).
class TouchInput {
public:
    /// How long a touch must be held to stop being a plain tap.
    static constexpr std::uint32_t kLongPressMs = 600;
    /// How long a touch must be held to (re)run the calibration.
    static constexpr std::uint32_t kRecalibrateHoldMs = 1500;
    /// A press must be released this long before the next one counts.
    static constexpr std::uint32_t kReleaseMs = 120;

    enum class Event : std::uint8_t { None, Press, Tap, LongPress, Hold };

    /// Feeds one sample. The events of a press come in this order, and only
    /// Press carries a new `position`; the others repeat the press-start one.
    Event update(bool pressed, TouchPoint position, std::uint32_t nowMs);

    /// The press-start position, valid from Press until the next one.
    TouchPoint position() const { return position_; }

private:
    enum class State : std::uint8_t { Idle, Held, Debounce };

    State state_ = State::Idle;
    bool longFired_ = false;
    bool holdFired_ = false;
    std::uint32_t pressMs_ = 0;
    TouchPoint position_;
};

/// A mapping from raw panel readings to screen positions, built from two
/// reference touches: one per-axis scale and offset each, which is what
/// hitting a card needs. The panel's orientation, mirror and per-unit drift
/// are absorbed by pairing the axes the way the two targets moved, so the
/// calibration never assumes one.
struct TouchCalibration {
    bool valid = false;
    TouchPoint rawA{};     ///< the raw reading over the first target
    TouchPoint screenA{};  ///< where that target was on the screen
    TouchPoint rawB{};
    TouchPoint screenB{};

    /// The screen position of a raw reading, clamped to the screen. Without a
    /// valid calibration it falls back to the panel's nominal full range, good
    /// enough for nothing but defined anyway.
    TouchPoint map(TouchPoint raw, std::int16_t width, std::int16_t height) const;
};

/// True when the two reference touches are far enough apart, on both axes,
/// for the scale they imply to be trusted.
bool calibrationIsSound(const TouchCalibration& calibration);

/// The two reference touches must be at least this many converter counts and
/// this many pixels apart, or the scale between them is a guess. On the
/// XPT2046 a pixel is several counts, so this only refuses touches on the
/// same spot.
inline constexpr std::int32_t kMinRawDelta = 128;
inline constexpr std::int32_t kMinScreenDelta = 24;

/// The calibration as the bytes a SettingsStore keeps, stamped with the screen
/// size it was taken in: another layout makes it meaningless. `capacity` must
/// be at least kCalibrationBlobSize; returns the length written, 0 if it cannot.
inline constexpr std::size_t kCalibrationBlobSize = 24;
std::size_t encodeCalibration(const TouchCalibration& calibration, std::int16_t width, std::int16_t height,
                              std::uint8_t* out, std::size_t capacity);
/// The reverse. False when `data` is not a calibration for a `width` x `height`
/// screen, so a stale or corrupted blob counts as uncalibrated.
bool decodeCalibration(const std::uint8_t* data, std::size_t length, std::int16_t width, std::int16_t height,
                       TouchCalibration& out);

}  // namespace managents::core
