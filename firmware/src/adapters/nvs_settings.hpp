#pragma once

#include <Preferences.h>

#include "managents/core/ports.hpp"

namespace managents::adapters {

/// core::SettingsStore in the board's NVS, through the Preferences library.
/// One namespace holds every key the core defines. The core writes only when
/// a setting changes — it skips frames that repeat what is already in
/// memory — so flash wear is a non-issue at this rate.
class NvsSettings : public core::SettingsStore {
public:
    /// Opens the namespace. Call it from setup(): NVS is initialized only
    /// inside initArduino(), so opening it from a global constructor — which
    /// runs before app_main — fails with ESP_ERR_NVS_NOT_INITIALIZED, and
    /// every later Preferences call silently no-ops, so nothing would persist.
    void init() { preferences_.begin("managents", false); }

    bool getBool(const char* key, bool fallback) override { return preferences_.getBool(key, fallback); }

    void putBool(const char* key, bool value) override { preferences_.putBool(key, value); }

    std::size_t getBlob(const char* key, void* out, std::size_t capacity) override {
        return preferences_.getBytes(key, out, capacity);
    }

    void putBlob(const char* key, const void* data, std::size_t length) override {
        preferences_.putBytes(key, data, length);
    }

private:
    Preferences preferences_;
};

}  // namespace managents::adapters
