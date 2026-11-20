#pragma once

#include <cstdint>

#include "managents/core/model.hpp"

namespace managents::core {

/// The short chime a card plays when it needs attention. Two patterns, so
/// waiting and error are tellable apart by ear alone.
enum class Chime : std::uint8_t { Waiting, Error };

/// The chime for the transition between the last applied `state` frame and the
/// new one. It plays when a card that was already on screen enters `waiting`
/// or `error`; Error wins if both happen in the same frame, so several cards
/// changing at once are one chime. Nothing plays for a card seen for the first
/// time, one that leaves the screen, one that keeps its status, or one that
/// decays to idle — only entering waiting or error is worth a sound, and the
/// silence repeats for as long as the card stays in it.
struct ChimeDecision {
    bool play = false;
    Chime which = Chime::Waiting;
};

ChimeDecision chimeFor(const HostState& previous, const HostState& current);

}  // namespace managents::core
