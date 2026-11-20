#include <unity.h>

#include <cstring>
#include <string>

#include "managents/core/protocol.hpp"
#include "managents/core/scene.hpp"
#include "managents/core/touch.hpp"

using namespace managents::core;

namespace {

const ScreenGeometry kGeometry{480, 320, 28, 6, 6};

// --- the touch state machine -------------------------------------------------

void a_quick_press_is_a_press_then_a_tap() {
    TouchInput touch;
    TEST_ASSERT_EQUAL(TouchInput::Event::Press, touch.update(true, {10, 10}, 0));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(true, {12, 11}, 100));
    TEST_ASSERT_EQUAL(TouchInput::Event::Tap, touch.update(false, {12, 11}, 200));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(false, {12, 11}, 210));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(false, {12, 11}, 400));
    TEST_ASSERT_TRUE((touch.position() == TouchPoint{10, 10}));  // where the press started
}

void a_held_press_becomes_a_long_press_exactly_once() {
    TouchInput touch;
    TEST_ASSERT_EQUAL(TouchInput::Event::Press, touch.update(true, {5, 5}, 0));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(true, {5, 5}, TouchInput::kLongPressMs - 1));
    TEST_ASSERT_EQUAL(TouchInput::Event::LongPress, touch.update(true, {5, 5}, TouchInput::kLongPressMs));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(true, {5, 5}, TouchInput::kLongPressMs + 500));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(false, {5, 5}, TouchInput::kLongPressMs + 900));
}

void a_hold_reaches_the_recalibration_threshold() {
    TouchInput touch;
    TEST_ASSERT_EQUAL(TouchInput::Event::Press, touch.update(true, {5, 5}, 0));
    TEST_ASSERT_EQUAL(TouchInput::Event::LongPress, touch.update(true, {5, 5}, TouchInput::kLongPressMs));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(true, {5, 5}, TouchInput::kRecalibrateHoldMs - 1));
    TEST_ASSERT_EQUAL(TouchInput::Event::Hold, touch.update(true, {5, 5}, TouchInput::kRecalibrateHoldMs));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(true, {5, 5}, TouchInput::kRecalibrateHoldMs + 100));
}

void a_bounce_right_after_a_release_is_not_a_press() {
    TouchInput touch;
    TEST_ASSERT_EQUAL(TouchInput::Event::Press, touch.update(true, {5, 5}, 0));
    TEST_ASSERT_EQUAL(TouchInput::Event::Tap, touch.update(false, {5, 5}, 40));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(true, {5, 5}, 80));  // 80 - 0 < kReleaseMs
    TEST_ASSERT_EQUAL(TouchInput::Event::Press, touch.update(true, {5, 5}, 130));
}

void a_press_after_a_long_press_counts_at_once() {
    TouchInput touch;
    TEST_ASSERT_EQUAL(TouchInput::Event::Press, touch.update(true, {5, 5}, 0));
    TEST_ASSERT_EQUAL(TouchInput::Event::LongPress, touch.update(true, {5, 5}, TouchInput::kLongPressMs));
    TEST_ASSERT_EQUAL(TouchInput::Event::None, touch.update(false, {5, 5}, TouchInput::kLongPressMs + 10));
    // The window is measured from the press start, so 700 ms later nothing is
    // pending: the next touch is a press of its own.
    TEST_ASSERT_EQUAL(TouchInput::Event::Press, touch.update(true, {5, 5}, TouchInput::kLongPressMs + 20));
}

// --- the calibration mapping --------------------------------------------------

void an_uncalibrated_display_maps_the_nominal_range() {
    TouchCalibration none;
    TEST_ASSERT_TRUE((none.map({0, 0}, 480, 320) == TouchPoint{0, 0}));
    TEST_ASSERT_TRUE((none.map({4095, 4095}, 480, 320) == TouchPoint{479, 319}));
    // Raw readings beyond the panel clamp to the screen.
    TEST_ASSERT_TRUE((none.map({6000, 9000}, 480, 320) == TouchPoint{479, 319}));
}

void two_diagonal_touches_map_the_whole_screen() {
    TouchCalibration calibration;
    calibration.rawA = {200, 800};
    calibration.screenA = calibrationTarget(0, kGeometry);  // (72, 64)
    calibration.rawB = {3400, 3100};
    calibration.screenB = calibrationTarget(1, kGeometry);  // (408, 256)
    calibration.valid = true;
    TEST_ASSERT_TRUE(calibrationIsSound(calibration));

    // The reference touches land on their targets, whatever the panel's axes.
    TEST_ASSERT_TRUE(calibration.map(calibration.rawA, 480, 320) == calibration.screenA);
    TEST_ASSERT_TRUE(calibration.map(calibration.rawB, 480, 320) == calibration.screenB);
    // The middle of the raw range lands between them, near the screen centre.
    const TouchPoint middle = calibration.map({1800, 1950}, 480, 320);
    TEST_ASSERT_INT_WITHIN(12, 240, middle.x);
    TEST_ASSERT_INT_WITHIN(12, 160, middle.y);
}

void the_axis_pairing_survives_a_swapped_or_mirrored_panel() {
    // The panel reports y where the screen expects x, and both run backwards:
    // the two reference touches still decide the pairing.
    TouchCalibration calibration;
    calibration.rawA = {3700, 300};
    calibration.screenA = calibrationTarget(0, kGeometry);
    calibration.rawB = {500, 3300};
    calibration.screenB = calibrationTarget(1, kGeometry);
    calibration.valid = true;
    TEST_ASSERT_TRUE(calibrationIsSound(calibration));
    TEST_ASSERT_TRUE(calibration.map(calibration.rawA, 480, 320) == calibration.screenA);
    TEST_ASSERT_TRUE(calibration.map(calibration.rawB, 480, 320) == calibration.screenB);
}

void an_unsound_calibration_is_refused() {
    TouchCalibration calibration;
    calibration.rawA = {200, 800};
    calibration.screenA = calibrationTarget(0, kGeometry);
    calibration.rawB = {250, 810};  // two touches on the same spot
    calibration.screenB = calibrationTarget(1, kGeometry);
    TEST_ASSERT_FALSE(calibrationIsSound(calibration));
}

void the_calibration_round_trips_through_the_blob() {
    TouchCalibration calibration;
    calibration.rawA = {200, 800};
    calibration.screenA = calibrationTarget(0, kGeometry);
    calibration.rawB = {3400, 3100};
    calibration.screenB = calibrationTarget(1, kGeometry);
    calibration.valid = true;

    std::uint8_t blob[kCalibrationBlobSize];
    TEST_ASSERT_EQUAL(kCalibrationBlobSize, encodeCalibration(calibration, 480, 320, blob, sizeof blob));

    TouchCalibration restored;
    TEST_ASSERT_TRUE(decodeCalibration(blob, sizeof blob, 480, 320, restored));
    TEST_ASSERT_TRUE(restored.valid);
    TEST_ASSERT_TRUE(calibrationIsSound(restored));
    TEST_ASSERT_TRUE(restored.map(calibration.rawA, 480, 320) == calibration.screenA);

    // Not for another screen size, and not from other bytes.
    TEST_ASSERT_FALSE(decodeCalibration(blob, sizeof blob, 320, 480, restored));
    TEST_ASSERT_FALSE(decodeCalibration(blob, sizeof blob - 1, 480, 320, restored));
    std::uint8_t corrupted[kCalibrationBlobSize];
    std::memcpy(corrupted, blob, sizeof blob);
    corrupted[2] = 2;
    TEST_ASSERT_FALSE(decodeCalibration(corrupted, sizeof corrupted, 480, 320, restored));
}

void refuses_to_encode_into_a_small_buffer() {
    TouchCalibration calibration;
    std::uint8_t tiny[8];
    TEST_ASSERT_EQUAL(0, encodeCalibration(calibration, 480, 320, tiny, sizeof tiny));
}

// --- hit-testing against the grid ---------------------------------------------

/// A state frame with `count` agents, decoded the way a real one arrives.
Scene sceneWith(std::size_t count) {
    std::string line = R"({"v":1,"t":"state","now":1000,"agents":[)";
    for (std::size_t i = 0; i < count; ++i) {
        line += std::string(i ? "," : "") + R"({"id":"c:)" + std::to_string(i) +
                R"(","kind":"claude","name":"n","status":"waiting","age":1})";
    }
    line += "]}";
    HostState host;
    Settings settings;
    TEST_ASSERT_EQUAL_MESSAGE(MessageType::State, decodeHostMessage(line.c_str(), line.size(), host, settings),
                              line.c_str());
    SceneInput input;
    input.host = &host;
    return buildScene(input, kGeometry);
}

void every_card_answers_its_own_rectangle() {
    for (std::size_t count : {1U, 2U, 4U, 6U, 7U, 9U}) {
        const Scene scene = sceneWith(count);
        for (std::uint8_t i = 0; i < scene.cardCount; ++i) {
            const Rect& bounds = scene.cards[i].bounds;
            const std::int8_t hit = cardAt(scene, bounds.x + bounds.w / 2, bounds.y + bounds.h / 2);
            TEST_ASSERT_EQUAL_INT8_MESSAGE(static_cast<std::int8_t>(i), hit, "a card missed its own centre");
        }
    }
}

void gaps_margins_and_the_header_are_not_cards() {
    const Scene scene = sceneWith(9);  // the compact 3 x 3 grid
    const Rect& first = scene.cards[0].bounds;
    const Rect& second = scene.cards[1].bounds;
    TEST_ASSERT_EQUAL_INT8(-1, cardAt(scene, second.x - 1, first.y + 1));  // the gap between two cards
    TEST_ASSERT_EQUAL_INT8(-1, cardAt(scene, 2, 160));                     // the margin
    TEST_ASSERT_EQUAL_INT8(-1, cardAt(scene, 240, 14));                    // the header, page dots included
    TEST_ASSERT_EQUAL_INT8(-1, cardAt(scene, 240, 8));                     // the +N badge shares the header
}

void the_overflow_and_empty_slots_answer_nothing() {
    const Scene scene = sceneWith(5);                     // two rows, three empty slots
    TEST_ASSERT_EQUAL_INT8(-1, cardAt(scene, 460, 300));  // an empty slot of the grid
    TEST_ASSERT_EQUAL_INT8(-1, cardAt(scene, 475, 8));
    const Scene onePage = sceneWith(2);
    TEST_ASSERT_EQUAL_INT8(-1, cardAt(onePage, 240, 14));  // no dots, but the header is still not a card
}

void the_calibration_targets_span_a_diagonal() {
    const TouchPoint first = calibrationTarget(0, kGeometry);
    const TouchPoint second = calibrationTarget(1, kGeometry);
    TEST_ASSERT_TRUE(first.x < second.x && first.y < second.y);
    // Far enough apart for the scales to be trusted, in both orientations.
    TEST_ASSERT_TRUE(calibrationIsSound([&] {
        TouchCalibration c;
        c.rawA = {200, 800};
        c.rawB = {3400, 3100};
        c.screenA = first;
        c.screenB = second;
        return c;
    }()));
    const ScreenGeometry portrait{320, 480, 28, 6, 6};
    const TouchPoint pFirst = calibrationTarget(0, portrait);
    const TouchPoint pSecond = calibrationTarget(1, portrait);
    TEST_ASSERT_TRUE(pFirst.x < pSecond.x && pFirst.y < pSecond.y);
}

}  // namespace

void setUp() {}
void tearDown() {}

int main() {
    UNITY_BEGIN();
    RUN_TEST(a_quick_press_is_a_press_then_a_tap);
    RUN_TEST(a_held_press_becomes_a_long_press_exactly_once);
    RUN_TEST(a_hold_reaches_the_recalibration_threshold);
    RUN_TEST(a_bounce_right_after_a_release_is_not_a_press);
    RUN_TEST(a_press_after_a_long_press_counts_at_once);
    RUN_TEST(an_uncalibrated_display_maps_the_nominal_range);
    RUN_TEST(two_diagonal_touches_map_the_whole_screen);
    RUN_TEST(the_axis_pairing_survives_a_swapped_or_mirrored_panel);
    RUN_TEST(an_unsound_calibration_is_refused);
    RUN_TEST(the_calibration_round_trips_through_the_blob);
    RUN_TEST(refuses_to_encode_into_a_small_buffer);
    RUN_TEST(every_card_answers_its_own_rectangle);
    RUN_TEST(gaps_margins_and_the_header_are_not_cards);
    RUN_TEST(the_overflow_and_empty_slots_answer_nothing);
    RUN_TEST(the_calibration_targets_span_a_diagonal);
    return UNITY_END();
}
