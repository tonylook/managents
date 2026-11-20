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
        : store(settings), app(display, indicator, link, kDevice, kGeometry, Pager{}, settings,
                               withSound ? &sound : nullptr) {}

    void receive(const std::string& line, std::uint32_t nowMs) {
        const std::string framed = line + "\n";
        app.onReceive(framed.data(), framed.size(), nowMs);
    }

    /// A tap: press, then release long enough for the next one to count.
    void tap(std::uint32_t nowMs) {
        app.onTouch(true, nowMs);
        app.onTouch(false, nowMs + 10);
        app.onTouch(false, nowMs + 10 + TapDetector::kReleaseMs);
    }

    SceneKind shown() const { return display.scenes.back().kind; }
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
    f.receive(stateWith("working"), 0);      // the first frame: nothing to transition from
    f.app.tick(0);
    TEST_ASSERT_EQUAL_size_t(0, f.sound.total());

    f.receive(stateWith("waiting"), 2000);   // working -> waiting
    f.app.tick(2000);
    TEST_ASSERT_EQUAL_size_t(1, f.sound.total());
    TEST_ASSERT_EQUAL(Chime::Waiting, f.sound.last());

    f.receive(stateWith("waiting"), 4000);   // stays waiting: silent
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

    f.app.onTouch(true, 200);
    f.app.onTouch(true, 210);  // still the same press
    f.app.tick(220);
    TEST_ASSERT_EQUAL_UINT8(1, f.display.scenes.back().header.page);
    TEST_ASSERT_EQUAL_UINT8(3, f.display.scenes.back().cardCount);

    f.app.onTouch(false, 300);
    f.app.onTouch(false, 500);
    f.app.onTouch(true, 600);  // a new press wraps around
    f.app.tick(600);
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
    RUN_TEST(summarizes_by_urgency);
    RUN_TEST(dims_only_screens_nobody_needs_to_read);
    RUN_TEST(dims_after_a_minute_of_reconnecting);
    RUN_TEST(dims_a_quiet_screen_and_a_tap_only_wakes_it);
    RUN_TEST(never_dims_the_setup_screen);
    return UNITY_END();
}
