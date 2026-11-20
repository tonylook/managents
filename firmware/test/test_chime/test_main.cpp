#include <unity.h>

#include <string>

#include "managents/core/chime.hpp"
#include "managents/core/protocol.hpp"

using namespace managents::core;

namespace {

/// A one-line state frame, decoded: the same path a real frame takes.
HostState stateOf(const std::string& agentsJson) {
    const std::string line =
        R"({"v":1,"t":"state","now":1000,"agents":[)" + agentsJson + "]}";
    HostState state;
    Settings settings;
    TEST_ASSERT_EQUAL_MESSAGE(MessageType::State, decodeHostMessage(line.c_str(), line.size(), state, settings),
                              line.c_str());
    return state;
}

std::string agent(const std::string& id, const char* status) {
    return R"({"id":")" + id + R"(","kind":"claude","name":"n","status":")" + status + R"(","age":0})";
}

ChimeDecision transition(const std::string& before, const std::string& after) {
    return chimeFor(stateOf(before), stateOf(after));
}

void entering_waiting_from_working_chimes() {
    const ChimeDecision decision = transition(agent("c:1", "working"), agent("c:1", "waiting"));
    TEST_ASSERT_TRUE(decision.play);
    TEST_ASSERT_EQUAL(Chime::Waiting, decision.which);
}

void entering_waiting_from_idle_chimes() {
    const ChimeDecision decision = transition(agent("c:1", "idle"), agent("c:1", "waiting"));
    TEST_ASSERT_TRUE(decision.play);
    TEST_ASSERT_EQUAL(Chime::Waiting, decision.which);
}

void entering_error_from_anything_chimes() {
    for (const char* before : {"working", "waiting", "idle"}) {
        const ChimeDecision decision = transition(agent("c:1", before), agent("c:1", "error"));
        TEST_ASSERT_TRUE_MESSAGE(decision.play, before);
        TEST_ASSERT_EQUAL_MESSAGE(Chime::Error, decision.which, before);
    }
}

void staying_in_a_state_never_chimes_again() {
    TEST_ASSERT_FALSE(transition(agent("c:1", "waiting"), agent("c:1", "waiting")).play);
    TEST_ASSERT_FALSE(transition(agent("c:1", "error"), agent("c:1", "error")).play);
    // Not even when the age reset (the helper restarts the count with the status).
    const std::string aged = R"({"id":"c:1","kind":"claude","name":"n","status":"waiting","age":5})";
    TEST_ASSERT_FALSE(transition(agent("c:1", "waiting"), aged).play);
}

void leaving_towards_working_or_idle_is_silent() {
    TEST_ASSERT_FALSE(transition(agent("c:1", "waiting"), agent("c:1", "working")).play);
    TEST_ASSERT_FALSE(transition(agent("c:1", "working"), agent("c:1", "idle")).play);  // the idle decay
    TEST_ASSERT_FALSE(transition(agent("c:1", "error"), agent("c:1", "working")).play);
}

void a_card_seen_for_the_first_time_is_not_a_transition() {
    // Boot, or the first frame after the link came back with one more session.
    TEST_ASSERT_FALSE(chimeFor(HostState{}, stateOf(agent("c:1", "waiting"))).play);
    TEST_ASSERT_FALSE(chimeFor(HostState{}, stateOf(agent("c:1", "error"))).play);
}

void a_card_that_leaves_the_screen_is_silent() {
    const ChimeDecision decision = transition(agent("c:1", "working") + "," + agent("c:2", "working"),
                                              agent("c:1", "working"));
    TEST_ASSERT_FALSE(decision.play);
}

void matching_is_by_id_not_by_name() {
    // Two sessions in one folder share the card name but not the id: a new
    // session arriving waiting must not chime for the one already waiting.
    const std::string before = agent("c:1", "waiting");
    const std::string after = before + "," + agent("c:2", "waiting");
    TEST_ASSERT_FALSE(transition(before, after).play);
}

void one_frame_with_several_changes_is_one_chime() {
    const std::string before = agent("c:1", "working") + "," + agent("c:2", "working");
    const std::string after = agent("c:1", "waiting") + "," + agent("c:2", "waiting");
    const ChimeDecision decision = transition(before, after);
    TEST_ASSERT_TRUE(decision.play);  // coalesced, not two
    TEST_ASSERT_EQUAL(Chime::Waiting, decision.which);
}

void an_error_in_the_same_frame_outranks_a_waiting() {
    const std::string before = agent("c:1", "working") + "," + agent("c:2", "working");
    const std::string after = agent("c:1", "waiting") + "," + agent("c:2", "error");
    const ChimeDecision decision = transition(before, after);
    TEST_ASSERT_TRUE(decision.play);
    TEST_ASSERT_EQUAL(Chime::Error, decision.which);
}

void nothing_is_playing_when_nothing_changed() {
    const std::string frame = agent("c:1", "working") + "," + agent("c:2", "waiting") + "," + agent("c:3", "idle");
    TEST_ASSERT_FALSE(chimeFor(stateOf(frame), stateOf(frame)).play);
    TEST_ASSERT_FALSE(chimeFor(HostState{}, HostState{}).play);
}

}  // namespace

void setUp() {}
void tearDown() {}

int main() {
    UNITY_BEGIN();
    RUN_TEST(entering_waiting_from_working_chimes);
    RUN_TEST(entering_waiting_from_idle_chimes);
    RUN_TEST(entering_error_from_anything_chimes);
    RUN_TEST(staying_in_a_state_never_chimes_again);
    RUN_TEST(leaving_towards_working_or_idle_is_silent);
    RUN_TEST(a_card_seen_for_the_first_time_is_not_a_transition);
    RUN_TEST(a_card_that_leaves_the_screen_is_silent);
    RUN_TEST(matching_is_by_id_not_by_name);
    RUN_TEST(one_frame_with_several_changes_is_one_chime);
    RUN_TEST(an_error_in_the_same_frame_outranks_a_waiting);
    RUN_TEST(nothing_is_playing_when_nothing_changed);
    return UNITY_END();
}
