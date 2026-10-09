#include <unity.h>

#include "managents/core/format.hpp"

using namespace managents::core;

namespace {

void formats_age_like_the_poc() {
    char text[16];
    formatAge(0, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("0s", text);
    formatAge(59, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("59s", text);
    formatAge(60, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("1m", text);
    formatAge(3599, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("59m", text);
    formatAge(3600 + 12 * 60 + 5, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("1h 12m", text);
}

void formats_clock_and_wraps_days() {
    char text[8];
    formatClock(0, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("00:00", text);
    formatClock(86400 * 3 + 13 * 3600 + 7 * 60 + 59, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("13:07", text);
    formatClock(-60, text, sizeof text);
    TEST_ASSERT_EQUAL_STRING("23:59", text);
}

void computes_clamped_percentages() {
    TEST_ASSERT_EQUAL_UINT8(44, percentOf(88000, 200000));
    TEST_ASSERT_EQUAL_UINT8(100, percentOf(300, 200));
    TEST_ASSERT_EQUAL_UINT8(0, percentOf(5, 0));
}

}  // namespace

void setUp() {}
void tearDown() {}

int main() {
    UNITY_BEGIN();
    RUN_TEST(formats_age_like_the_poc);
    RUN_TEST(formats_clock_and_wraps_days);
    RUN_TEST(computes_clamped_percentages);
    return UNITY_END();
}
