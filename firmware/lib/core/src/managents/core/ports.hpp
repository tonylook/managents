#pragma once

#include <cstddef>
#include <cstdint>

#include "managents/core/model.hpp"
#include "managents/core/scene.hpp"

namespace managents::core {

// Ports: the boundaries between the hardware-independent core and the board.
// Adapters in firmware/src implement them; tests implement them with fakes.

enum class Brightness : std::uint8_t { Full, Dimmed };

/// Paints a scene on the panel and drives its backlight.
class Display {
public:
    virtual ~Display() = default;
    virtual void present(const Scene& scene) = 0;
    /// Called on every tick; adapters touch the backlight only when it changes.
    virtual void setBrightness(Brightness brightness) = 0;
};

/// A secondary indicator (the board's RGB LED).
class StatusIndicator {
public:
    virtual ~StatusIndicator() = default;
    /// Called on every tick. `blinkOn` is the blink phase for an Error that
    /// should blink; it stays true once the error is past its first minute.
    virtual void show(Attention attention, bool blinkOn) = 0;
};

/// Writes protocol lines back to the host.
class HostLink {
public:
    virtual ~HostLink() = default;
    /// `line` is a complete message without the trailing newline.
    virtual void sendLine(const char* line) = 0;
};

/// A small persistent key-value store for what the display keeps across
/// reboots: the sound setting now, the touch calibration later. Implemented by
/// the board's NVS (the Preferences library); tests use fakes. The core owns
/// the key names; the adapter namespaces them.
class SettingsStore {
public:
    virtual ~SettingsStore() = default;
    /// Returns the stored value, or `fallback` when the key is absent.
    virtual bool getBool(const char* key, bool fallback) = 0;
    virtual void putBool(const char* key, bool value) = 0;
    /// Copies at most `capacity` bytes of the stored value into `out` and
    /// returns their number; 0 when the key is absent or longer.
    virtual std::size_t getBlob(const char* key, void* out, std::size_t capacity) = 0;
    virtual void putBlob(const char* key, const void* data, std::size_t length) = 0;
};

/// Key of the sound setting under SettingsStore.
inline constexpr const char* kSoundSettingKey = "sound";

/// Whether the display plays a short chime when an agent starts waiting or
/// fails. Off unless the computer turned it on (a `settings` frame); the
/// choice survives a reboot.
inline constexpr bool kSoundByDefault = false;

}  // namespace managents::core
