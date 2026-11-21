#include <unity.h>

#include <cstring>
#include <string>

#include "managents/core/protocol.hpp"

using namespace managents::core;

namespace {

MessageType decode(const std::string& line, HostState& state) {
    return decodeHostMessage(line.c_str(), line.size(), state);
}

MessageType decode(const std::string& line, HostState& state, Settings& settings) {
    return decodeHostMessage(line.c_str(), line.size(), state, settings);
}

const char* kTwoAgents =
    R"({"v":1,"t":"state","now":1759230000,"tz":7200,"agents":[)"
    R"({"id":"claude:1","kind":"claude","name":"billing-api","status":"working","age":42,)"
    R"("ctx":{"used":88000,"limit":200000}},)"
    R"({"id":"opencode:2","kind":"opencode","name":"core","status":"idle","age":9000,"ctx":null}]})";

void decodes_a_state_frame() {
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::State, decode(kTwoAgents, state));
    TEST_ASSERT_EQUAL_INT64(1759230000, state.hostEpochSeconds);
    TEST_ASSERT_EQUAL_INT32(7200, state.utcOffsetSeconds);
    TEST_ASSERT_EQUAL_UINT8(2, state.agentCount);

    const Agent& claude = state.agents[0];
    TEST_ASSERT_EQUAL_STRING("claude:1", claude.id.c_str());
    TEST_ASSERT_EQUAL_STRING("billing-api", claude.name.c_str());
    TEST_ASSERT_EQUAL(AgentKind::Claude, claude.kind);
    TEST_ASSERT_EQUAL(AgentStatus::Working, claude.status);
    TEST_ASSERT_EQUAL_UINT32(42, claude.ageSeconds);
    TEST_ASSERT_TRUE(claude.context.known);
    TEST_ASSERT_EQUAL_UINT32(200000, claude.context.limit);

    const Agent& opencode = state.agents[1];
    TEST_ASSERT_EQUAL(AgentKind::OpenCode, opencode.kind);
    TEST_ASSERT_EQUAL(AgentStatus::Idle, opencode.status);
    TEST_ASSERT_FALSE(opencode.context.known);
}

void decodes_every_known_kind() {
    HostState state;
    const char* line = R"({"v":1,"t":"state","now":1,"agents":[)"
                       R"({"id":"claude:1","kind":"claude","name":"a","status":"working","age":0},)"
                       R"({"id":"opencode:2","kind":"opencode","name":"b","status":"working","age":0},)"
                       R"({"id":"codex:3","kind":"codex","name":"c","status":"working","age":0},)"
                       R"({"id":"zcode:4","kind":"zcode","name":"d","status":"working","age":0},)"
                       R"({"id":"qwen:5","kind":"qwen","name":"e","status":"working","age":0},)"
                       R"({"id":"gemini:6","kind":"gemini","name":"f","status":"working","age":0},)"
                       R"({"id":"pi:7","kind":"pi","name":"g","status":"working","age":0},)"
                       R"({"id":"droid:8","kind":"droid","name":"h","status":"working","age":0},)"
                       R"({"id":"crush:9","kind":"crush","name":"i","status":"working","age":0},)"
                       R"({"id":"goose:10","kind":"goose","name":"j","status":"working","age":0},)"
                       R"({"id":"amp:11","kind":"amp","name":"k","status":"working","age":0},)"
                       R"({"id":"omp:12","kind":"omp","name":"l","status":"working","age":0}]})";
    TEST_ASSERT_EQUAL(MessageType::State, decode(line, state));
    TEST_ASSERT_EQUAL_UINT8(12, state.agentCount);
    const AgentKind kinds[] = {AgentKind::Claude, AgentKind::OpenCode, AgentKind::Codex, AgentKind::ZCode,
                               AgentKind::Qwen,   AgentKind::Gemini,   AgentKind::Pi,    AgentKind::Droid,
                               AgentKind::Crush,  AgentKind::Goose,    AgentKind::Amp,   AgentKind::Omp};
    for (std::uint8_t i = 0; i < 12; ++i) {
        TEST_ASSERT_EQUAL_MESSAGE(kinds[i], state.agents[i].kind, state.agents[i].id.c_str());
    }
}

void decodes_an_empty_frame_with_defaults() {
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::State, decode(R"({"v":1,"t":"state","now":5,"agents":[]})", state));
    TEST_ASSERT_EQUAL_UINT8(0, state.agentCount);
    TEST_ASSERT_EQUAL_INT32(0, state.utcOffsetSeconds);
    TEST_ASSERT_EQUAL_UINT16(0, state.moreCount);
}

void keeps_unknown_kinds_and_fields() {
    HostState state;
    const char* line = R"({"v":1,"t":"state","now":1,"future":true,"agents":[)"
                       R"({"id":"x:1","kind":"aider","name":"n","status":"waiting","age":0,"extra":[1,2]}]})";
    TEST_ASSERT_EQUAL(MessageType::State, decode(line, state));
    TEST_ASSERT_EQUAL(AgentKind::Unknown, state.agents[0].kind);
}

void caps_agents_and_counts_the_rest() {
    std::string line = R"({"v":1,"t":"state","now":1,"more":2,"agents":[)";
    for (int i = 0; i < 26; ++i) {
        line += std::string(i ? "," : "") + R"({"id":"c:)" + std::to_string(i) +
                R"(","kind":"claude","name":"a","status":"working","age":1})";
    }
    line += "]}";
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::State, decode(line, state));
    TEST_ASSERT_EQUAL_UINT8(kMaxAgents, state.agentCount);
    TEST_ASSERT_EQUAL_UINT16(4, state.moreCount);
}

void clamps_the_overflow_badge() {
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::State, decode(R"({"v":1,"t":"state","now":1,"more":70000,"agents":[]})", state));
    TEST_ASSERT_EQUAL_UINT16(0xFFFF, state.moreCount);
    // Beyond size_t on the ESP32, where it is 32 bits wide.
    TEST_ASSERT_EQUAL(MessageType::State,
                      decode(R"({"v":1,"t":"state","now":1,"more":5000000000,"agents":[]})", state));
    TEST_ASSERT_EQUAL_UINT16(0xFFFF, state.moreCount);
}

void saturates_numbers_beyond_32_bits() {
    HostState state;
    const char* line = R"({"v":1,"t":"state","now":1,"agents":[{"id":"c:1","kind":"claude","name":"a",)"
                       R"("status":"working","age":5000000000,"ctx":{"used":5000000000,"limit":6000000000}}]})";
    TEST_ASSERT_EQUAL(MessageType::State, decode(line, state));
    TEST_ASSERT_EQUAL_UINT32(0xFFFFFFFF, state.agents[0].ageSeconds);
    TEST_ASSERT_EQUAL_UINT32(0xFFFFFFFF, state.agents[0].context.used);
    TEST_ASSERT_EQUAL_UINT32(0xFFFFFFFF, state.agents[0].context.limit);
}

void truncates_long_names_on_a_character_boundary() {
    std::string name = "a";  // puts the cut at the capacity inside an "é"
    for (int i = 0; i < 40; ++i) {
        name += "\xC3\xA9";  // "é", two bytes
    }
    const std::string line = R"({"v":1,"t":"state","now":1,"agents":[{"id":"c:1","kind":"claude","name":")" + name +
                             R"(","status":"working","age":1}]})";
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::State, decode(line, state));
    const auto& stored = state.agents[0].name;
    TEST_ASSERT_EQUAL(stored.capacity() - 1, stored.size());
    TEST_ASSERT_EQUAL_STRING(name.substr(0, stored.size()).c_str(), stored.c_str());
}

void rejects_malformed_lines_without_touching_state() {
    // Malformed frames live in protocol/fixtures/invalid, shared with the helper
    // (test_contract). These are the lines a fixture file cannot express.
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::State, decode(kTwoAgents, state));

    TEST_ASSERT_EQUAL(MessageType::Invalid, decode("", state));
    TEST_ASSERT_EQUAL(MessageType::Invalid, decode("   ", state));
    // Only `length` bytes count, not the terminator: here they leave out the closing brace.
    const std::string frame = kTwoAgents;
    TEST_ASSERT_EQUAL(MessageType::Invalid, decodeHostMessage(frame.c_str(), frame.size() - 1, state));

    TEST_ASSERT_EQUAL_UINT8(2, state.agentCount);
    TEST_ASSERT_EQUAL_STRING("billing-api", state.agents[0].name.c_str());
}

void ignores_other_versions_and_types() {
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::Ignored, decode(R"({"v":2,"t":"state","now":1,"agents":[]})", state));
    TEST_ASSERT_EQUAL(MessageType::Ignored, decode(R"({"v":1,"t":"config","brightness":10})", state));
}

void recognises_hello_probe() {
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::Hello, decode(R"({"v":1,"t":"hello"})", state));
}

void decodes_a_settings_frame() {
    HostState state;
    Settings settings;
    TEST_ASSERT_EQUAL(MessageType::Settings, decode(R"({"v":1,"t":"settings","sound":true})", state, settings));
    TEST_ASSERT_TRUE(settings.hasSound);
    TEST_ASSERT_TRUE(settings.sound);

    TEST_ASSERT_EQUAL(MessageType::Settings, decode(R"({"v":1,"t":"settings","sound":false})", state, settings));
    TEST_ASSERT_TRUE(settings.hasSound);
    TEST_ASSERT_FALSE(settings.sound);
}

void a_settings_frame_keeps_unknown_fields_and_state() {
    // Unknown fields are ignored (docs/protocol.md): future settings arrive
    // without breaking this firmware. A state decoded earlier must survive.
    HostState state;
    TEST_ASSERT_EQUAL(MessageType::State, decode(kTwoAgents, state));
    Settings settings;
    TEST_ASSERT_EQUAL(MessageType::Settings,
                      decode(R"({"v":1,"t":"settings","sound":true,"brightness":9})", state, settings));
    TEST_ASSERT_TRUE(settings.hasSound);
    TEST_ASSERT_EQUAL_UINT8(2, state.agentCount);
    TEST_ASSERT_EQUAL_STRING("billing-api", state.agents[0].name.c_str());
}

void rejects_settings_frames_without_a_known_field() {
    HostState state;
    Settings settings;  // a rejected frame changes nothing
    TEST_ASSERT_EQUAL(MessageType::Invalid, decode(R"({"v":1,"t":"settings"})", state, settings));
    TEST_ASSERT_EQUAL(MessageType::Invalid, decode(R"({"v":1,"t":"settings","future":1})", state, settings));
    TEST_ASSERT_EQUAL(MessageType::Invalid, decode(R"({"v":1,"t":"settings","sound":"yes"})", state, settings));
    TEST_ASSERT_EQUAL(MessageType::Invalid, decode(R"({"v":1,"t":"settings","sound":1})", state, settings));
    TEST_ASSERT_FALSE(settings.hasSound);
}

void encodes_hello() {
    const DeviceInfo info{"managents", "0.1.0", "e32r40t", 480, 320};
    char line[192];
    const std::size_t length = encodeHello(info, line, sizeof line);
    TEST_ASSERT_EQUAL(std::strlen(line), length);
    TEST_ASSERT_EQUAL_STRING(
        R"({"v":1,"t":"hello","device":"managents","fw":"0.1.0","board":"e32r40t","w":480,"h":320,"proto":1})", line);
}

void refuses_to_encode_into_a_small_buffer() {
    const DeviceInfo info{"managents", "0.1.0", "e32r40t", 480, 320};
    char line[16];
    TEST_ASSERT_EQUAL(0, encodeHello(info, line, sizeof line));
}

}  // namespace

void setUp() {}
void tearDown() {}

int main() {
    UNITY_BEGIN();
    RUN_TEST(decodes_a_state_frame);
    RUN_TEST(decodes_an_empty_frame_with_defaults);
    RUN_TEST(decodes_every_known_kind);
    RUN_TEST(keeps_unknown_kinds_and_fields);
    RUN_TEST(caps_agents_and_counts_the_rest);
    RUN_TEST(clamps_the_overflow_badge);
    RUN_TEST(saturates_numbers_beyond_32_bits);
    RUN_TEST(truncates_long_names_on_a_character_boundary);
    RUN_TEST(rejects_malformed_lines_without_touching_state);
    RUN_TEST(ignores_other_versions_and_types);
    RUN_TEST(recognises_hello_probe);
    RUN_TEST(decodes_a_settings_frame);
    RUN_TEST(a_settings_frame_keeps_unknown_fields_and_state);
    RUN_TEST(rejects_settings_frames_without_a_known_field);
    RUN_TEST(encodes_hello);
    RUN_TEST(refuses_to_encode_into_a_small_buffer);
    return UNITY_END();
}
