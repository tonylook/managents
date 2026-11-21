#include <unity.h>

#include <cstring>
#include <map>
#include <string>
#include <vector>

#include "managents/core/application.hpp"

using namespace managents::core;

namespace {

class FakeDisplay : public Display {
public:
    void present(const Scene& scene) override { scenes.push_back(scene); }
    void setBrightness(Brightness value) override { brightness = value; }
    std::vector<Scene> scenes;
    Brightness brightness = Brightness::Full;
};

class FakeIndicator : public StatusIndicator {
public:
    void show(Attention value, bool blink) override {
        attention = value;
        blinkOn = blink;
    }
    Attention attention = Attention::Disconnected;
    bool blinkOn = true;
};

class FakeLink : public HostLink {
public:
    void sendLine(const char* line) override { lines.emplace_back(line); }
    std::vector<std::string> lines;
};

class FakeStore : public SettingsStore {
public:
    bool getBool(const char* key, bool fallback) override {
        const auto entry = bools.find(key);
        return entry == bools.end() ? fallback : entry->second;
    }
    void putBool(const char* key, bool value) override {
        bools[key] = value;
        ++writes;
    }
    std::size_t getBlob(const char* key, void* out, std::size_t capacity) override {
        const auto entry = blobs.find(key);
        if (entry == blobs.end() || entry->second.size() > capacity) {
            return 0;
        }
        std::memcpy(out, entry->second.data(), entry->second.size());
        return entry->second.size();
    }
    void putBlob(const char* key, const void* data, std::size_t length) override {
        blobs[key] = std::string(static_cast<const char*>(data), length);
        ++writes;
    }

    std::map<std::string, bool> bools;
    std::map<std::string, std::string> blobs;
    int writes = 0;
};

class FakeSound : public SoundOutput {
public:
    void play(Chime chime) override { history.push_back(chime); }
    std::size_t total() const { return history.size(); }
    std::size_t countOf(Chime which) const {
        std::size_t count = 0;
        for (const Chime played : history) {
            count += played == which ? 1 : 0;
        }
        return count;
    }
    Chime last() const { return history.back(); }
    std::vector<Chime> history;
};

const DeviceInfo kDevice{"managents", "0.1.0", "e32r40t", 480, 320};
const ScreenGeometry kGeometry{480, 320, 28, 6, 6};
const std::string kHello = R"({"v":1,"t":"hello"})";

struct Fixture {
    FakeDisplay display;
    FakeIndicator indicator;
    FakeLink link;
    FakeStore* store = nullptr;  // may be null: nothing persists
    FakeSound sound;
    Application app;

    explicit Fixture(FakeStore* settings = nullptr, bool withSound = false)
        : store(settings),
          app(display, indicator, link, kDevice, kGeometry, Pager{}, settings, withSound ? &sound : nullptr) {}

    void receive(const std::string& line, std::uint32_t nowMs) {
        const std::string framed = line + "\n";
        app.onReceive(framed.data(), framed.size(), nowMs);
    }

    /// A tap at a point: press, release, and one more release sample after the
    /// debounce so a following press counts as its own. The tap is recognised
    /// on the release: the same press could have become a long press.
    void tap(std::uint32_t nowMs, TouchPoint raw = kCardCentre) {
        app.onTouch(true, raw, nowMs);
        app.onTouch(false, raw, nowMs + 10);
        app.onTouch(false, raw, nowMs + 10 + TouchInput::kReleaseMs);
    }

    /// A long press at a point: held past the threshold — sampled every 10 ms,
    /// the way the loop does — then released.
    void longPress(std::uint32_t nowMs, TouchPoint raw = kCardCentre) {
        for (std::uint32_t t = nowMs; t <= nowMs + TouchInput::kLongPressMs; t += 10) {
            app.onTouch(true, raw, t);
        }
        app.onTouch(false, raw, nowMs + TouchInput::kLongPressMs + 10);
    }

    /// A hold at a point: kept down past the recalibration threshold.
    void hold(std::uint32_t nowMs, TouchPoint raw = kCardCentre) {
        for (std::uint32_t t = nowMs; t <= nowMs + TouchInput::kRecalibrateHoldMs; t += 10) {
            app.onTouch(true, raw, t);
        }
        app.onTouch(false, raw, nowMs + TouchInput::kRecalibrateHoldMs + 10);
    }

    SceneKind shown() const { return display.scenes.back().kind; }

    /// The centre of the first card of a 2 x 2 grid on the 480 x 320 fixture.
    static constexpr TouchPoint kCardCentre{120, 90};
};

std::string stateWithAgents(int count, const char* status, std::uint32_t age = 0) {
    std::string line = R"({"v":1,"t":"state","now":1000,"agents":[)";
    for (int i = 0; i < count; ++i) {
        line += std::string(i ? "," : "") + R"({"id":"c:)" + std::to_string(i) +
                R"(","kind":"claude","name":"a","status":")" + status + R"(","age":)" + std::to_string(age) + "}";
    }
    return line + "]}";
}

std::string stateWith(const char* status, std::uint32_t age = 0) {
    return stateWithAgents(1, status, age);
}

/// Like stateWithAgents, but every agent is called name-<i>, so a test can tell
/// which agent a detail view is showing.
std::string stateWithNamedAgents(int count, const char* status, std::uint32_t age = 0) {
    std::string line = R"({"v":1,"t":"state","now":1000,"agents":[)";
    for (int i = 0; i < count; ++i) {
        line += std::string(i ? "," : "") + R"({"id":"c:)" + std::to_string(i) +
                R"(","kind":"claude","name":"name-)" + std::to_string(i) + R"(","status":")" + status +
                R"(","age":)" + std::to_string(age) + "}";
    }
    return line + "]}";
}

void announces_itself_and_shows_connecting_at_boot() {
    Fixture f;
    f.app.begin(0);
    TEST_ASSERT_EQUAL(1, f.link.lines.size());
    TEST_ASSERT_TRUE(f.link.lines[0].find(R"("device":"managents")") != std::string::npos);
    TEST_ASSERT_EQUAL(1, f.display.scenes.size());
    TEST_ASSERT_EQUAL(SceneKind::Connecting, f.shown());
    TEST_ASSERT_EQUAL(Attention::Disconnected, f.indicator.attention);
}

void answers_hello_probes() {
    Fixture f;
    f.app.begin(0);
    f.receive(kHello, 10);
    TEST_ASSERT_EQUAL(2, f.link.lines.size());
}

void suggests_setup_after_fifteen_seconds_without_the_helper() {
    Fixture f;
    f.app.begin(0);
    f.app.tick(Application::kSetupHintAfterMs - 1);
    TEST_ASSERT_EQUAL(SceneKind::Connecting, f.shown());
    f.app.tick(Application::kSetupHintAfterMs);
    TEST_ASSERT_EQUAL(SceneKind::SetupNeeded, f.shown());

    f.receive(stateWith("working"), 20000);  // installed after all
    f.app.tick(20000);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
}

void a_hello_probe_counts_as_contact() {
    Fixture f;
    f.app.begin(0);
    f.receive(kHello, 14000);
    f.app.tick(15000);
    TEST_ASSERT_EQUAL(SceneKind::Connecting, f.shown());
    f.app.tick(14000 + Application::kSetupHintAfterMs);  // probing, but never a frame
    TEST_ASSERT_EQUAL(SceneKind::SetupNeeded, f.shown());
    f.receive(kHello, 30000);
    f.app.tick(30000);
    TEST_ASSERT_EQUAL(SceneKind::Connecting, f.shown());
}

void shows_agents_after_a_state_frame() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("waiting"), 100);
    f.app.tick(100);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    TEST_ASSERT_EQUAL(Attention::Waiting, f.indicator.attention);
}

void redraws_only_on_visible_change() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 100);
    f.app.tick(100);
    const std::size_t presented = f.display.scenes.size();
    f.app.tick(200);
    f.app.tick(600);
    TEST_ASSERT_EQUAL(presented, f.display.scenes.size());
    f.app.tick(1100);  // age ticks from 0s to 1s
    TEST_ASSERT_EQUAL(presented + 1, f.display.scenes.size());
}

void shows_reconnecting_after_silence_and_recovers() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 1000);
    f.app.tick(1000 + Application::kLinkTimeoutMs - 1);
    TEST_ASSERT_TRUE(f.app.hostConnected(1000 + Application::kLinkTimeoutMs - 1));
    f.app.tick(1000 + Application::kLinkTimeoutMs);
    TEST_ASSERT_EQUAL(SceneKind::Reconnecting, f.shown());
    TEST_ASSERT_EQUAL(Attention::Disconnected, f.indicator.attention);

    f.app.tick(60000);  // a lost link never turns into the setup hint
    TEST_ASSERT_EQUAL(SceneKind::Reconnecting, f.shown());

    f.receive(stateWith("working"), 61000);
    f.app.tick(61000);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
}

void hello_and_garbage_do_not_keep_the_link_alive() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 0);
    for (std::uint32_t t = 1000; t <= 7000; t += 1000) {
        f.receive(kHello, t);
        f.receive("{broken", t);
        f.app.tick(t);
    }
    TEST_ASSERT_EQUAL(SceneKind::Reconnecting, f.shown());
}

void ignores_another_protocol_version() {
    Fixture f;
    f.app.begin(0);
    f.receive(R"({"v":2,"t":"state","now":1000,"agents":[]})", 100);
    f.app.tick(100);
    TEST_ASSERT_EQUAL(SceneKind::Connecting, f.shown());
}

void a_stale_frame_never_comes_back_after_the_millis_wrap() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 1000);
    f.app.tick(10000);
    TEST_ASSERT_EQUAL(SceneKind::Reconnecting, f.shown());
    f.app.tick(2000);  // 2^32 ms later: 1000 ms after the frame again
    TEST_ASSERT_FALSE(f.app.hostConnected(2000));
    TEST_ASSERT_EQUAL(SceneKind::Reconnecting, f.shown());
}

void the_setup_hint_survives_the_millis_wrap() {
    Fixture f;
    f.app.begin(0);
    f.app.tick(Application::kSetupHintAfterMs);
    f.app.tick(1000);  // 2^32 ms later
    TEST_ASSERT_EQUAL(SceneKind::SetupNeeded, f.shown());
}

void keeps_the_link_across_the_millis_wrap() {
    Fixture f;
    f.app.begin(0xFFFFF000u);
    f.receive(stateWith("working"), 0xFFFFF000u);
    f.app.tick(0xFFFFF000u + 5000u);  // wraps to 904
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    TEST_ASSERT_EQUAL_STRING("5s", f.display.scenes.back().cards[0].age.c_str());
}

void ignores_garbage_between_frames() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 100);
    f.receive("ets Jun  8 2016 00:22:57 rst:0x1 (POWERON_RESET)", 150);
    f.receive("{broken", 200);
    f.app.tick(200);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    TEST_ASSERT_EQUAL(Attention::Working, f.indicator.attention);
}

void a_settings_frame_stores_the_sound_setting() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 100);
    TEST_ASSERT_EQUAL_INT(1, store.writes);
    TEST_ASSERT_TRUE(store.bools[kSoundSettingKey]);
    f.receive(R"({"v":1,"t":"settings","sound":false})", 200);
    TEST_ASSERT_FALSE(store.bools[kSoundSettingKey]);
    // Without a store nothing breaks: the setting applies for this run only.
    Fixture bare;
    bare.app.begin(0);
    bare.receive(R"({"v":1,"t":"settings","sound":true})", 100);
}

// The sender repeats its settings frame for as long as it runs: only a change
// may reach the store, or the flash wears out on identical frames.
void identical_settings_frames_write_the_store_once() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 100);  // a change from the default
    TEST_ASSERT_EQUAL_INT(1, store.writes);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 200);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 300);
    TEST_ASSERT_EQUAL_INT(1, store.writes);
    f.receive(R"({"v":1,"t":"settings","sound":false})", 400);  // a change still writes
    TEST_ASSERT_EQUAL_INT(2, store.writes);
    TEST_ASSERT_FALSE(store.bools[kSoundSettingKey]);
}

// The value in memory comes from the store at boot, not from the default: a
// frame repeating what a previous run stored writes nothing, and a real
// change after boot still does.
void a_setting_read_from_the_store_writes_only_on_a_change() {
    FakeStore store;
    store.bools[kSoundSettingKey] = true;  // what a previous run stored
    Fixture f(&store);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 100);
    TEST_ASSERT_EQUAL_INT(0, store.writes);
    f.receive(R"({"v":1,"t":"settings","sound":false})", 200);
    TEST_ASSERT_EQUAL_INT(1, store.writes);
    TEST_ASSERT_FALSE(store.bools[kSoundSettingKey]);
}

void a_settings_frame_says_nothing_about_the_link() {
    // The sound setting is sent on the sender's own connection, which may be
    // the only contact while the service is stopped: it must not silence the
    // setup hint or keep the link alive.
    Fixture f;
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 100);
    f.app.tick(Application::kSetupHintAfterMs);
    TEST_ASSERT_EQUAL(SceneKind::SetupNeeded, f.shown());

    Fixture g;
    g.app.begin(0);
    g.receive(stateWith("working"), 0);
    g.receive(R"({"v":1,"t":"settings","sound":true})", Application::kLinkTimeoutMs);
    g.app.tick(Application::kLinkTimeoutMs + 100);
    TEST_ASSERT_EQUAL(SceneKind::Reconnecting, g.shown());
}

void a_transition_into_waiting_chimes_once_when_enabled() {
    Fixture f(nullptr, true);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 0);
    f.receive(stateWith("working"), 0);  // the first frame: nothing to transition from
    f.app.tick(0);
    TEST_ASSERT_EQUAL_size_t(0, f.sound.total());

    f.receive(stateWith("waiting"), 2000);  // working -> waiting
    f.app.tick(2000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
    TEST_ASSERT_EQUAL(Chime::Waiting, f.sound.last());

    f.receive(stateWith("waiting"), 4000);  // stays waiting: silent
    f.receive(stateWith("waiting"), 6000);
    f.app.tick(6000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
}

void an_error_transition_chimes_the_error_pattern() {
    Fixture f(nullptr, true);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 0);
    f.receive(stateWith("working"), 0);
    f.receive(stateWith("error"), 2000);
    f.app.tick(2000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
    TEST_ASSERT_EQUAL(Chime::Error, f.sound.last());
}

void a_recovering_agent_chimes_waiting_again() {
    // error -> working -> waiting is two transitions, one chime each. The
    // first frame (working here) never chimes: nothing transitioned.
    Fixture f(nullptr, true);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 0);
    f.receive(stateWith("working"), 0);
    f.receive(stateWith("error"), 2000);
    f.receive(stateWith("working"), 4000);
    f.receive(stateWith("waiting"), 6000);
    f.app.tick(6000);
    TEST_ASSERT_EQUAL_size_t(2, f.sound.total());
    TEST_ASSERT_EQUAL_size_t(1, f.sound.countOf(Chime::Error));
    TEST_ASSERT_EQUAL_size_t(1, f.sound.countOf(Chime::Waiting));
}

void coalesced_transitions_are_one_chime() {
    Fixture f(nullptr, true);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 0);
    f.receive(stateWithAgents(2, "working"), 0);
    f.receive(stateWithAgents(2, "waiting"), 2000);
    f.app.tick(2000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
}

void the_chime_plays_only_when_the_setting_turned_it_on() {
    Fixture f(nullptr, true);  // default off (kSoundByDefault)
    f.app.begin(0);
    f.receive(stateWith("working"), 0);
    f.receive(stateWith("waiting"), 2000);
    f.app.tick(2000);
    TEST_ASSERT_EQUAL_size_t(0, f.sound.total());

    f.receive(R"({"v":1,"t":"settings","sound":true})", 3000);
    f.receive(stateWith("working"), 4000);
    f.receive(stateWith("waiting"), 6000);
    f.app.tick(6000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());

    f.receive(R"({"v":1,"t":"settings","sound":false})", 7000);
    f.receive(stateWith("working"), 8000);
    f.receive(stateWith("waiting"), 10000);
    f.app.tick(10000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
}

void the_sound_setting_survives_a_reboot() {
    FakeStore store;
    {
        Fixture f(&store, true);
        f.app.begin(0);
        f.receive(R"({"v":1,"t":"settings","sound":true})", 100);
        TEST_ASSERT_TRUE(store.bools[kSoundSettingKey]);
    }
    // A fresh Application over the same store: the setting comes back.
    Fixture f(&store, true);
    f.app.begin(0);
    f.receive(stateWith("working"), 0);
    f.receive(stateWith("waiting"), 2000);
    f.app.tick(2000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
}

void a_dimmed_screen_stays_quiet_but_wakes() {
    Fixture f(nullptr, true);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 0);
    std::uint32_t t = 0;
    auto idleFor = [&](std::uint32_t ms) {
        for (const std::uint32_t end = t + ms; t < end; t += 2000) {
            f.receive(stateWith("idle"), t);
            f.app.tick(t);
        }
    };
    idleFor(kDimWhileQuietAfterMs);
    f.receive(stateWith("idle"), t);  // the frame at the threshold itself latches the dim
    f.app.tick(t);
    TEST_ASSERT_EQUAL(Brightness::Dimmed, f.display.brightness);

    // The agent needs attention while nobody is watching: the screen wakes so
    // the state is visible on return, but the chime stays quiet (ADR-0014).
    f.receive(stateWith("waiting"), t + 100);
    f.app.tick(t + 100);
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);
    TEST_ASSERT_EQUAL_size_t(0, f.sound.total());

    // The next transition, now that the screen is awake, is heard.
    f.receive(stateWith("working"), t + 300);
    f.receive(stateWith("waiting"), t + 500);
    f.app.tick(t + 500);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
}

void taps_and_page_flips_never_chime() {
    Fixture f(nullptr, true);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 0);
    f.receive(stateWithAgents(12, "working"), 100);
    f.app.tick(100);
    f.tap(200);
    f.app.tick(400);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);  // the tap paged
    TEST_ASSERT_EQUAL_size_t(0, f.sound.total());                     // and nothing played
}

void the_led_blinks_only_during_the_first_minute_of_an_error() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("error", 58), 0);
    f.app.tick(500);  // dark phase, error 58 s old
    TEST_ASSERT_EQUAL(Attention::Error, f.indicator.attention);
    TEST_ASSERT_FALSE(f.indicator.blinkOn);
    TEST_ASSERT_TRUE(f.display.scenes.back().cards[0].alertPhase);

    f.app.tick(2500);  // dark phase again, the error is now 60 s old: steady
    TEST_ASSERT_EQUAL(Attention::Error, f.indicator.attention);
    TEST_ASSERT_TRUE(f.indicator.blinkOn);
    TEST_ASSERT_FALSE(f.display.scenes.back().cards[0].alertPhase);
}

void a_tap_anywhere_shows_the_next_page() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWithAgents(12, "waiting"), 100);
    f.app.tick(100);
    TEST_ASSERT_EQUAL_UINT8(9, f.display.scenes.back().cardCount);

    f.app.onTouch(true, {240, 160}, 200);
    f.app.onTouch(true, {240, 160}, 210);  // still the same press
    f.app.tick(220);
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().header.page);  // not yet: the tap is the release

    f.app.onTouch(false, {240, 160}, 300);
    f.app.tick(300);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);
    TEST_ASSERT_EQUAL_UINT8(3, f.display.scenes.back().cardCount);

    f.app.onTouch(false, {240, 160}, 500);
    f.app.onTouch(true, {240, 160}, 600);  // a new press wraps around
    f.app.onTouch(false, {240, 160}, 610);
    f.app.tick(700);
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().header.page);
}

void a_tap_while_disconnected_does_nothing() {
    Fixture f;
    f.app.begin(0);
    f.tap(100);
    f.app.tick(400);
    TEST_ASSERT_EQUAL(1, f.display.scenes.size());
    TEST_ASSERT_EQUAL(SceneKind::Connecting, f.shown());
}

// The detail view and the calibration (ADR-0014). The touch tests below use a
// four-agent frame: a 2 x 2 grid whose first card spans x 6..237, y 28..168,
// so kCardCentre (120, 90) is on it and (240, 14) is the header.

std::string stateFour() {
    return stateWithAgents(4, "working");
}

/// Feeds the two calibration samples as raw readings straight at the targets:
/// the mapping that comes out maps raw to screen one to one. The taps are more
/// than kReleaseMs apart, so neither counts as a bounce.
void completeCalibration(Fixture& f, std::uint32_t& t) {
    f.longPress(t, Fixture::kCardCentre);
    f.app.tick(t += TouchInput::kLongPressMs + 10);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());
    for (std::size_t step = 0; step < kCalibrationSteps; ++step) {
        f.tap(t += 200, calibrationTarget(step, kGeometry));
        f.app.tick(t += 10);
    }
}

void an_uncalibrated_long_press_offers_the_calibration() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    f.longPress(400);
    f.app.tick(600);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().calibration.step);
    TEST_ASSERT_TRUE(f.display.scenes.back().calibration.target == calibrationTarget(0, kGeometry));

    // Leaving it (the hold-to-cancel) returns to the grid, where a plain tap
    // keeps paging exactly as before.
    f.hold(800);
    f.app.tick(2400);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    f.receive(stateWithAgents(12, "working"), 2500);
    f.app.tick(2500);
    f.tap(2700);
    f.app.tick(2900);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);
}

void plain_tap_paging_works_before_during_and_after() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateWithAgents(12, "working"), 0);
    f.app.tick(0);
    f.tap(200);
    f.app.tick(400);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);  // before: uncalibrated

    f.longPress(600);  // mid-calibration the taps are the samples
    f.app.tick(800);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());
    f.tap(1000, calibrationTarget(0, kGeometry));
    f.app.tick(1100);
    f.tap(1300, calibrationTarget(1, kGeometry));
    f.app.tick(1400);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());  // done

    f.tap(1600);
    f.app.tick(1800);
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().header.page);  // after: paging again
}

void a_completed_calibration_is_stored_and_the_next_long_press_details() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    TEST_ASSERT_EQUAL_INT(1, store.writes);
    TEST_ASSERT_TRUE(store.blobs.count(kTouchCalibrationKey) == 1);

    f.longPress(t += 300);  // the same press position: now it hits a card
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());
    const DetailView detail = f.display.scenes.back().detail;  // a copy: more scenes may come
    TEST_ASSERT_EQUAL(AgentKind::Claude, detail.kind);
    TEST_ASSERT_EQUAL(AgentStatus::Working, detail.status);
    TEST_ASSERT_EQUAL_STRING("a", detail.name.c_str());
    TEST_ASSERT_EQUAL_STRING("1s", detail.age.c_str());         // the frame is 1.7 s old
    TEST_ASSERT_EQUAL_STRING("00:16", detail.started.c_str());  // now 1000, tz 0, minus the age
}

void the_calibration_survives_a_reboot() {
    FakeStore store;
    {
        Fixture f(&store);
        f.app.begin(0);
        f.receive(stateFour(), 0);
        f.app.tick(0);
        std::uint32_t t = 400;
        completeCalibration(f, t);
    }
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    f.longPress(600);
    f.app.tick(800);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());  // no calibration first: it was remembered
}

void a_calibration_for_another_screen_counts_as_none() {
    FakeStore store;
    TouchCalibration calibrated;
    calibrated.rawA = {200, 800};
    calibrated.screenA = {72, 64};
    calibrated.rawB = {3400, 3100};
    calibrated.screenB = {408, 256};
    calibrated.valid = true;
    std::uint8_t blob[kCalibrationBlobSize];
    TEST_ASSERT_EQUAL(kCalibrationBlobSize, encodeCalibration(calibrated, 320, 480, blob, sizeof blob));  // portrait
    store.blobs[kTouchCalibrationKey] = std::string(reinterpret_cast<const char*>(blob), kCalibrationBlobSize);

    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    f.longPress(400);
    f.app.tick(600);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());  // recalibrate, never trust it
}

void an_unsound_calibration_is_not_kept() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    f.longPress(400);
    f.app.tick(500);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());
    const TouchPoint sameSpot{200, 800};
    f.tap(600, sameSpot);
    f.app.tick(700);
    f.tap(900, sameSpot);  // both samples on one spot: no scale to trust
    f.app.tick(1000);
    TEST_ASSERT_EQUAL_INT(0, store.writes);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    f.longPress(1200);
    f.app.tick(1400);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());  // still uncalibrated
}

void holding_to_cancel_skips_the_calibration() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    f.longPress(400);
    f.app.tick(500);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());
    f.tap(600, calibrationTarget(0, kGeometry));  // the first sample
    f.app.tick(700);
    f.longPress(900);  // held instead of the second target: cancel
    f.app.tick(1600);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    TEST_ASSERT_EQUAL_INT(0, store.writes);  // nothing was kept

    f.longPress(1800);
    f.app.tick(2000);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().calibration.step);  // from the start again
}

void a_long_press_on_a_card_opens_the_detail_view() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    f.longPress(t += 300);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());
}

void a_long_press_off_the_cards_does_nothing() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    const std::size_t before = f.display.scenes.size();

    f.longPress(t += 300, {240, 14});  // the header, page dots included
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    f.longPress(t += 300, {3, 160});  // the margin
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    f.longPress(t += 300, {239, 90});  // the gap between the two cards
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    (void)before;
}

void a_tap_or_a_long_press_dismisses_the_detail() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    f.longPress(t += 300);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());

    f.tap(t += 300);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().header.page);  // back, not paged

    f.longPress(t += 300);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());
    f.longPress(t += 300);  // long as well
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
}

void the_detail_view_follows_the_agent_by_id() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    f.longPress(t += 300);  // card 0, agent "c:0"
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());

    // The helper reorders: the view keeps showing the agent it opened for.
    const std::string reordered = R"({"v":1,"t":"state","now":1000,"agents":[)"
                                  R"({"id":"c:2","kind":"claude","name":"other","status":"working","age":1},)"
                                  R"({"id":"c:0","kind":"codex","name":"a","status":"waiting","age":9},)"
                                  R"({"id":"c:1","kind":"claude","name":"n","status":"working","age":2}]})";
    f.receive(reordered, t += 2000);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());
    TEST_ASSERT_EQUAL(AgentStatus::Waiting, f.display.scenes.back().detail.status);
    TEST_ASSERT_EQUAL_STRING("9s", f.display.scenes.back().detail.age.c_str());

    // The agent's session closes (two other sessions remain): the grid returns
    // by itself.
    const std::string closed = R"({"v":1,"t":"state","now":1000,"agents":[)"
                               R"({"id":"d:1","kind":"claude","name":"other","status":"working","age":1},)"
                               R"({"id":"d:2","kind":"claude","name":"also","status":"working","age":2}]})";
    f.receive(closed, t += 2000);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
}

void a_link_drop_closes_the_detail_view() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    f.longPress(t += 300);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());

    f.app.tick(t += Application::kLinkTimeoutMs + 100);
    TEST_ASSERT_EQUAL(SceneKind::Reconnecting, f.shown());
    f.receive(stateFour(), t += 1000);  // the computer woke up
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());  // the grid, not the detail
}

void the_detail_view_keeps_the_ages_ticking() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    f.longPress(t += 300);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());
    const std::size_t presented = f.display.scenes.size();

    f.app.tick(t + 1000);
    TEST_ASSERT_TRUE(f.display.scenes.size() > presented);
    TEST_ASSERT_EQUAL_STRING("2s", f.display.scenes.back().detail.age.c_str());
}

void holding_a_moment_and_a_half_recalibrates_anywhere() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateFour(), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);
    const std::string blob = store.blobs[kTouchCalibrationKey];

    f.hold(t += 300, {240, 14});  // even off the cards
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Calibration, f.shown());

    // Leaving the flow without finishing keeps the old calibration.
    f.longPress(t += 300);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());
    TEST_ASSERT_TRUE(store.blobs[kTouchCalibrationKey] == blob);
}

void a_press_on_a_dimmed_screen_only_wakes_it() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"settings","sound":true})", 0);
    f.receive(stateWithAgents(12, "idle"), 0);
    std::uint32_t t = 0;
    for (; t < kDimWhileQuietAfterMs; t += 2000) {
        f.receive(stateWithAgents(12, "idle"), t);
        f.app.tick(t);
    }
    f.receive(stateWithAgents(12, "idle"), t);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(Brightness::Dimmed, f.display.brightness);

    f.longPress(t + 100);
    f.app.tick(t + 800);
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);  // woken
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.shown());            // not the calibration
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().header.page);

    f.tap(t + 2000);
    f.app.tick(t + 2200);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);  // the next tap pages
}

void a_long_press_held_across_a_page_flip_details_the_card_it_hit() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateWithAgents(12, "working"), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);

    f.tap(t += 200);  // page 2, where the same position holds a different card
    f.app.tick(t + 100);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);
    const CardView hit = f.display.scenes.back().cards[0];  // a copy: more scenes may come

    // The press starts close enough to the 30 s return that the page flips
    // while the finger is down; the long press fires on the page it hit. A
    // fresh frame keeps the link alive across the seconds before it.
    const std::uint32_t press = t + Pager::kReturnToFirstMs - 590;
    f.receive(stateWithAgents(12, "working"), press - 1000);
    f.app.tick(press - 1000);
    f.app.onTouch(true, Fixture::kCardCentre, press);
    f.app.onTouch(true, Fixture::kCardCentre, press + 599);
    f.app.tick(press + 599);                                 // still page 2, still on time
    f.app.onTouch(true, Fixture::kCardCentre, press + 600);  // the threshold passes
    f.app.tick(press + 600);                                 // this tick returns to page 1
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());
    TEST_ASSERT_EQUAL_STRING(hit.name.c_str(), f.display.scenes.back().detail.name.c_str());
}

void a_long_press_on_a_later_page_details_the_card_pressed() {
    FakeStore store;
    Fixture f(&store);
    f.app.begin(0);
    f.receive(stateWithNamedAgents(12, "working"), 0);
    f.app.tick(0);
    std::uint32_t t = 400;
    completeCalibration(f, t);

    f.tap(t += 200);  // the second page: agents 9..11
    f.app.tick(t + 100);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);

    // The page's first card is agent 9: the detail must show the agent the
    // finger was on, not the one the page-local index names (name-0).
    const CardView hit = f.display.scenes.back().cards[0];  // a copy: more scenes may come
    const TouchPoint centre{static_cast<std::int16_t>(hit.bounds.x + hit.bounds.w / 2),
                            static_cast<std::int16_t>(hit.bounds.y + hit.bounds.h / 2)};
    f.longPress(t += 400, centre);
    f.app.tick(t);
    TEST_ASSERT_EQUAL(SceneKind::Detail, f.shown());
    TEST_ASSERT_EQUAL_STRING("name-9", f.display.scenes.back().detail.name.c_str());
}

void summarizes_by_urgency() {
    HostState host;
    host.agentCount = 3;
    host.agents[0].status = AgentStatus::Idle;
    host.agents[1].status = AgentStatus::Working;
    host.agents[2].status = AgentStatus::Idle;
    TEST_ASSERT_EQUAL(Attention::Working, summarize(host));
    host.agents[2].status = AgentStatus::Waiting;
    TEST_ASSERT_EQUAL(Attention::Waiting, summarize(host));
    host.agents[1].status = AgentStatus::Waiting;
    host.agents[2].status = AgentStatus::Working;  // a later working agent does not hide a waiting one
    TEST_ASSERT_EQUAL(Attention::Waiting, summarize(host));
    host.agents[0].status = AgentStatus::Error;
    TEST_ASSERT_EQUAL(Attention::Error, summarize(host));
    host.agentCount = 0;
    TEST_ASSERT_EQUAL(Attention::Quiet, summarize(host));
}

void dims_only_screens_nobody_needs_to_read() {
    struct Case {
        SceneKind kind;
        Attention attention;
        std::uint32_t msUnchanged;
        bool dims;
    };
    const Case cases[] = {
        {SceneKind::Agents, Attention::Quiet, kDimWhileQuietAfterMs - 1, false},
        {SceneKind::Agents, Attention::Quiet, kDimWhileQuietAfterMs, true},
        {SceneKind::NoAgents, Attention::Quiet, kDimWhileQuietAfterMs, true},
        {SceneKind::Agents, Attention::Working, 0xFFFFFFFF, false},
        {SceneKind::Agents, Attention::Waiting, 0xFFFFFFFF, false},
        {SceneKind::Agents, Attention::Error, 0xFFFFFFFF, false},
        {SceneKind::Reconnecting, Attention::Disconnected, kDimWhileAsleepAfterMs - 1, false},
        {SceneKind::Reconnecting, Attention::Disconnected, kDimWhileAsleepAfterMs, true},
        {SceneKind::Connecting, Attention::Disconnected, 0xFFFFFFFF, false},
        {SceneKind::SetupNeeded, Attention::Disconnected, 0xFFFFFFFF, false},
    };
    for (const Case& c : cases) {
        TEST_ASSERT_EQUAL(c.dims, dimsBacklight(c.kind, c.attention, c.msUnchanged));
    }
}

void dims_after_a_minute_of_reconnecting() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 0);
    const std::uint32_t lost = Application::kLinkTimeoutMs;
    f.app.tick(lost);
    f.app.tick(lost + kDimWhileAsleepAfterMs - 1);
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);
    f.app.tick(lost + kDimWhileAsleepAfterMs);
    TEST_ASSERT_EQUAL(Brightness::Dimmed, f.display.brightness);

    f.receive(stateWith("working"), lost + kDimWhileAsleepAfterMs + 10);  // the computer wakes up
    f.app.tick(lost + kDimWhileAsleepAfterMs + 10);
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);
}

void dims_a_quiet_screen_and_a_tap_only_wakes_it() {
    Fixture f;
    f.app.begin(0);
    std::uint32_t t = 0;
    auto idleFor = [&](std::uint32_t ms) {
        for (const std::uint32_t end = t + ms; t < end; t += 2000) {
            f.receive(stateWithAgents(12, "idle"), t);
            f.app.tick(t);
        }
        f.receive(stateWithAgents(12, "idle"), t);
        f.app.tick(t);
    };
    idleFor(kDimWhileQuietAfterMs - 2000);
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);
    idleFor(2000);
    TEST_ASSERT_EQUAL(Brightness::Dimmed, f.display.brightness);

    f.tap(t + 100);
    f.app.tick(t + 300);
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);
    TEST_ASSERT_EQUAL_UINT8(0, f.display.scenes.back().header.page);  // woken, not paged

    f.receive(stateWithAgents(12, "working"), t + 400);
    f.app.tick(t + 400);
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);
}

void never_dims_the_setup_screen() {
    Fixture f;
    f.app.begin(0);
    f.app.tick(Application::kSetupHintAfterMs);
    f.app.tick(3600u * 1000u);
    TEST_ASSERT_EQUAL(SceneKind::SetupNeeded, f.shown());
    TEST_ASSERT_EQUAL(Brightness::Full, f.display.brightness);
}

}  // namespace

void setUp() {}
void tearDown() {}

int main() {
    UNITY_BEGIN();
    RUN_TEST(announces_itself_and_shows_connecting_at_boot);
    RUN_TEST(answers_hello_probes);
    RUN_TEST(suggests_setup_after_fifteen_seconds_without_the_helper);
    RUN_TEST(a_hello_probe_counts_as_contact);
    RUN_TEST(shows_agents_after_a_state_frame);
    RUN_TEST(redraws_only_on_visible_change);
    RUN_TEST(shows_reconnecting_after_silence_and_recovers);
    RUN_TEST(hello_and_garbage_do_not_keep_the_link_alive);
    RUN_TEST(ignores_another_protocol_version);
    RUN_TEST(a_stale_frame_never_comes_back_after_the_millis_wrap);
    RUN_TEST(the_setup_hint_survives_the_millis_wrap);
    RUN_TEST(keeps_the_link_across_the_millis_wrap);
    RUN_TEST(ignores_garbage_between_frames);
    RUN_TEST(a_settings_frame_stores_the_sound_setting);
    RUN_TEST(identical_settings_frames_write_the_store_once);
    RUN_TEST(a_setting_read_from_the_store_writes_only_on_a_change);
    RUN_TEST(a_settings_frame_says_nothing_about_the_link);
    RUN_TEST(a_transition_into_waiting_chimes_once_when_enabled);
    RUN_TEST(an_error_transition_chimes_the_error_pattern);
    RUN_TEST(a_recovering_agent_chimes_waiting_again);
    RUN_TEST(coalesced_transitions_are_one_chime);
    RUN_TEST(the_chime_plays_only_when_the_setting_turned_it_on);
    RUN_TEST(the_sound_setting_survives_a_reboot);
    RUN_TEST(a_dimmed_screen_stays_quiet_but_wakes);
    RUN_TEST(taps_and_page_flips_never_chime);
    RUN_TEST(the_led_blinks_only_during_the_first_minute_of_an_error);
    RUN_TEST(a_tap_anywhere_shows_the_next_page);
    RUN_TEST(a_tap_while_disconnected_does_nothing);
    RUN_TEST(an_uncalibrated_long_press_offers_the_calibration);
    RUN_TEST(plain_tap_paging_works_before_during_and_after);
    RUN_TEST(a_completed_calibration_is_stored_and_the_next_long_press_details);
    RUN_TEST(the_calibration_survives_a_reboot);
    RUN_TEST(a_calibration_for_another_screen_counts_as_none);
    RUN_TEST(an_unsound_calibration_is_not_kept);
    RUN_TEST(holding_to_cancel_skips_the_calibration);
    RUN_TEST(a_long_press_on_a_card_opens_the_detail_view);
    RUN_TEST(a_long_press_off_the_cards_does_nothing);
    RUN_TEST(a_tap_or_a_long_press_dismisses_the_detail);
    RUN_TEST(the_detail_view_follows_the_agent_by_id);
    RUN_TEST(a_link_drop_closes_the_detail_view);
    RUN_TEST(the_detail_view_keeps_the_ages_ticking);
    RUN_TEST(holding_a_moment_and_a_half_recalibrates_anywhere);
    RUN_TEST(a_press_on_a_dimmed_screen_only_wakes_it);
    RUN_TEST(a_long_press_held_across_a_page_flip_details_the_card_it_hit);
    RUN_TEST(a_long_press_on_a_later_page_details_the_card_pressed);
    RUN_TEST(summarizes_by_urgency);
    RUN_TEST(dims_only_screens_nobody_needs_to_read);
    RUN_TEST(dims_after_a_minute_of_reconnecting);
    RUN_TEST(dims_a_quiet_screen_and_a_tap_only_wakes_it);
    RUN_TEST(never_dims_the_setup_screen);
    return UNITY_END();
}
