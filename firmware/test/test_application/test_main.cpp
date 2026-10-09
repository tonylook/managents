#include <unity.h>

#include <string>
#include <vector>

#include "managents/core/application.hpp"

using namespace managents::core;

namespace {

class FakeDisplay : public Display {
public:
    void present(const Scene& scene) override { scenes.push_back(scene); }
    std::vector<Scene> scenes;
};

class FakeIndicator : public StatusIndicator {
public:
    void show(Attention value, bool) override { attention = value; }
    Attention attention = Attention::Disconnected;
};

class FakeLink : public HostLink {
public:
    void sendLine(const char* line) override { lines.emplace_back(line); }
    std::vector<std::string> lines;
};

const DeviceInfo kDevice{"managents", "0.1.0", "e32r40t", 480, 320};
const ScreenGeometry kGeometry{480, 320, 28, 6, 6};

struct Fixture {
    FakeDisplay display;
    FakeIndicator indicator;
    FakeLink link;
    Application app{display, indicator, link, kDevice, kGeometry};

    void receive(const std::string& line, std::uint32_t nowMs) {
        const std::string framed = line + "\n";
        app.onReceive(framed.data(), framed.size(), nowMs);
    }
};

std::string stateWith(const char* status) {
    return std::string(R"({"v":1,"t":"state","now":1000,"agents":[{"id":"c:1","kind":"claude","name":"a","status":")") +
           status + R"(","age":0}]})";
}

void announces_itself_and_waits_for_host_at_boot() {
    Fixture f;
    f.app.begin(0);
    TEST_ASSERT_EQUAL(1, f.link.lines.size());
    TEST_ASSERT_TRUE(f.link.lines[0].find(R"("device":"managents")") != std::string::npos);
    TEST_ASSERT_EQUAL(1, f.display.scenes.size());
    TEST_ASSERT_EQUAL(SceneKind::WaitingForHost, f.display.scenes.back().kind);
    TEST_ASSERT_EQUAL(Attention::Disconnected, f.indicator.attention);
}

void answers_hello_probes() {
    Fixture f;
    f.app.begin(0);
    f.receive(R"({"v":1,"t":"hello"})", 10);
    TEST_ASSERT_EQUAL(2, f.link.lines.size());
}

void shows_agents_after_a_state_frame() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("waiting"), 100);
    f.app.tick(100);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.display.scenes.back().kind);
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

void falls_back_to_waiting_screen_after_silence() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 1000);
    f.app.tick(1000 + Application::kLinkTimeoutMs - 1);
    TEST_ASSERT_TRUE(f.app.hostConnected(1000 + Application::kLinkTimeoutMs - 1));
    f.app.tick(1000 + Application::kLinkTimeoutMs);
    TEST_ASSERT_EQUAL(SceneKind::WaitingForHost, f.display.scenes.back().kind);
    TEST_ASSERT_EQUAL(Attention::Disconnected, f.indicator.attention);

    f.receive(stateWith("working"), 20000);
    f.app.tick(20000);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.display.scenes.back().kind);
}

void ignores_garbage_between_frames() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWith("working"), 100);
    f.receive("ets Jun  8 2016 00:22:57 rst:0x1 (POWERON_RESET)", 150);
    f.receive("{broken", 200);
    f.app.tick(200);
    TEST_ASSERT_EQUAL(SceneKind::Agents, f.display.scenes.back().kind);
    TEST_ASSERT_EQUAL(Attention::Working, f.indicator.attention);
}

std::string stateWithAgents(int count) {
    std::string line = R"({"v":1,"t":"state","now":1000,"agents":[)";
    for (int i = 0; i < count; ++i) {
        line += std::string(i ? "," : "") + R"({"id":"c:)" + std::to_string(i) +
                R"(","kind":"claude","name":"a","status":"waiting","age":0})";
    }
    return line + "]}";
}

void a_tap_anywhere_shows_the_next_page() {
    Fixture f;
    f.app.begin(0);
    f.receive(stateWithAgents(12), 100);
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

void summarizes_by_urgency() {
    HostState host;
    host.agentCount = 3;
    host.agents[0].status = AgentStatus::Idle;
    host.agents[1].status = AgentStatus::Working;
    host.agents[2].status = AgentStatus::Idle;
    TEST_ASSERT_EQUAL(Attention::Working, summarize(host));
    host.agents[2].status = AgentStatus::Waiting;
    TEST_ASSERT_EQUAL(Attention::Waiting, summarize(host));
    host.agents[0].status = AgentStatus::Error;
    TEST_ASSERT_EQUAL(Attention::Error, summarize(host));
    host.agentCount = 0;
    TEST_ASSERT_EQUAL(Attention::Quiet, summarize(host));
}

}  // namespace

void setUp() {}
void tearDown() {}

int main() {
    UNITY_BEGIN();
    RUN_TEST(announces_itself_and_waits_for_host_at_boot);
    RUN_TEST(answers_hello_probes);
    RUN_TEST(shows_agents_after_a_state_frame);
    RUN_TEST(redraws_only_on_visible_change);
    RUN_TEST(falls_back_to_waiting_screen_after_silence);
    RUN_TEST(ignores_garbage_between_frames);
    RUN_TEST(a_tap_anywhere_shows_the_next_page);
    RUN_TEST(summarizes_by_urgency);
    return UNITY_END();
}
