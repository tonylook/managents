// Contract tests: the firmware must accept every line in protocol/fixtures/valid
// and reject every line in protocol/fixtures/invalid — the same files the helper
// validates against the JSON Schema.

#include <unity.h>

#include <dirent.h>

#include <fstream>
#include <string>
#include <vector>

#include "managents/core/protocol.hpp"

using namespace managents::core;

namespace {

// `pio test` runs from the firmware directory.
const std::string kFixtures = "../protocol/fixtures/";

std::vector<std::string> linesIn(const std::string& directory) {
    std::vector<std::string> lines;
    DIR* dir = opendir((kFixtures + directory).c_str());
    TEST_ASSERT_NOT_NULL_MESSAGE(dir, "fixture directory not found");
    while (const dirent* entry = readdir(dir)) {
        const std::string name = entry->d_name;
        if (name.size() < 6 || name.substr(name.size() - 6) != ".jsonl") {
            continue;
        }
        std::ifstream file(kFixtures + directory + "/" + name);
        for (std::string line; std::getline(file, line);) {
            if (!line.empty()) {
                lines.push_back(line);
            }
        }
    }
    closedir(dir);
    TEST_ASSERT_FALSE_MESSAGE(lines.empty(), "no fixture lines");
    return lines;
}

void accepts_valid_fixtures() {
    for (const std::string& line : linesIn("valid")) {
        HostState state;
        const MessageType type = decodeHostMessage(line.c_str(), line.size(), state);
        TEST_ASSERT_TRUE_MESSAGE(type == MessageType::State || type == MessageType::Hello, line.c_str());
    }
}

void rejects_invalid_fixtures() {
    for (const std::string& line : linesIn("invalid")) {
        HostState state;
        TEST_ASSERT_EQUAL_MESSAGE(MessageType::Invalid, decodeHostMessage(line.c_str(), line.size(), state),
                                  line.c_str());
    }
}

}  // namespace

void setUp() {}
void tearDown() {}

int main() {
    UNITY_BEGIN();
    RUN_TEST(accepts_valid_fixtures);
    RUN_TEST(rejects_invalid_fixtures);
    return UNITY_END();
}
