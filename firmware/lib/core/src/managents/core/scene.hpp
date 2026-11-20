#pragma once

#include <cstddef>
#include <cstdint>

#include "managents/core/fixed_string.hpp"
#include "managents/core/grid_layout.hpp"
#include "managents/core/model.hpp"
#include "managents/core/pager.hpp"
#include "managents/core/touch.hpp"

namespace managents::core {

/// What the screen should show, fully resolved: positions, texts and states.
/// The renderer only paints a Scene; it never looks at protocol data.

enum class SceneKind : std::uint8_t {
    Connecting,    ///< waiting for the helper's first frame
    SetupNeeded,   ///< no sign of the helper for a while: how to set it up
    Reconnecting,  ///< frames used to arrive and stopped (computer asleep?)
    NoAgents,
    Agents,
    Detail,       ///< one card, held longer than a plain tap (ADR-0014)
    Calibration,  ///< the guided touch calibration, offered lazily
};

/// Context-window fill, drawn as a bar along the bottom of the card. Shown only
/// when the host knows both the usage and the limit.
struct ContextView {
    bool visible = false;
    std::uint8_t percent = 0;  ///< 0..100

    bool operator==(const ContextView& other) const { return visible == other.visible && percent == other.percent; }
};

struct CardView {
    Rect bounds;
    AgentKind kind = AgentKind::Unknown;
    AgentStatus status = AgentStatus::Waiting;
    bool alertPhase = false;  ///< a fresh error card blinks: true while in the dark phase
    FixedString<64> name;     ///< printable ASCII only
    FixedString<12> age;
    ContextView context;

    bool operator==(const CardView& other) const {
        return bounds == other.bounds && kind == other.kind && status == other.status &&
               alertPhase == other.alertPhase && name == other.name && age == other.age && context == other.context;
    }
    bool operator!=(const CardView& other) const { return !(*this == other); }
};

struct HeaderView {
    static constexpr std::size_t kMaxPages = (kMaxAgents + Pager::kPerPage - 1) / Pager::kPerPage;
    static_assert(kMaxPages == 3, "pageAttention below starts Quiet on every page");

    Rect bounds;
    FixedString<8> clock;          ///< "HH:MM"
    FixedString<8> overflowBadge;  ///< "+3" when the host had more agents than it could send
    std::uint8_t page = 0;         ///< current page, 0-based
    std::uint8_t pageCount = 1;
    /// What each page needs, so the page dots can point at errors and waiting
    /// agents that are not on screen. Only the first `pageCount` entries count.
    Attention pageAttention[kMaxPages] = {Attention::Quiet, Attention::Quiet, Attention::Quiet};

    bool operator==(const HeaderView& other) const {
        if (pageCount != other.pageCount) {
            return false;
        }
        for (std::size_t i = 0; i < pageCount; ++i) {
            if (pageAttention[i] != other.pageAttention[i]) {
                return false;
            }
        }
        return bounds == other.bounds && clock == other.clock && overflowBadge == other.overflowBadge &&
               page == other.page;
    }
    bool operator!=(const HeaderView& other) const { return !(*this == other); }
};

/// One card held longer than a plain tap (ADR-0014): everything the protocol
/// carries for the agent, laid out to be read rather than glanced.
struct DetailView {
    Rect bounds;
    AgentKind kind = AgentKind::Unknown;
    AgentStatus status = AgentStatus::Waiting;
    FixedString<64> name;    ///< the folder name, folded to ASCII as on the card
    FixedString<12> age;     ///< how long the agent has been in this status
    FixedString<8> started;  ///< "HH:MM", when this status started, from the host clock and the age
    ContextView context;

    bool operator==(const DetailView& other) const {
        return bounds == other.bounds && kind == other.kind && status == other.status && name == other.name &&
               age == other.age && started == other.started && context == other.context;
    }
    bool operator!=(const DetailView& other) const { return !(*this == other); }
};

/// The guided touch calibration (ADR-0014): one target at a time, touched in
/// turn. Never shown at boot; a long press on an uncalibrated display offers it.
struct CalibrationView {
    std::uint8_t step = 0;  ///< which of the targets is being taken
    TouchPoint target;      ///< where the circle is drawn

    bool operator==(const CalibrationView& other) const { return step == other.step && target == other.target; }
    bool operator!=(const CalibrationView& other) const { return !(*this == other); }
};

struct Scene {
    SceneKind kind = SceneKind::Connecting;
    HeaderView header;
    CardView cards[Pager::kPerPage];  ///< the current page only
    std::uint8_t cardCount = 0;
    DetailView detail;            ///< what the detail view shows, when kind is Detail
    CalibrationView calibration;  ///< the target to touch, when kind is Calibration

    bool operator==(const Scene& other) const {
        if (kind != other.kind || header != other.header || cardCount != other.cardCount || detail != other.detail ||
            calibration != other.calibration) {
            return false;
        }
        for (std::uint8_t i = 0; i < cardCount; ++i) {
            if (cards[i] != other.cards[i]) {
                return false;
            }
        }
        return true;
    }
    bool operator!=(const Scene& other) const { return !(*this == other); }
};

/// Screen geometry the scene is laid out for.
struct ScreenGeometry {
    std::int16_t width = 0;
    std::int16_t height = 0;
    std::int16_t headerHeight = 0;
    std::int16_t margin = 0;  ///< around the card grid
    std::int16_t gap = 0;     ///< between cards
};

/// The geometry the firmware lays its scenes out with on a `width` x `height` screen.
constexpr ScreenGeometry screenGeometry(std::int16_t width, std::int16_t height) {
    return {width, height, /*headerHeight=*/28, /*margin=*/6, /*gap=*/6};
}

/// Why there is no live host state to show.
enum class LinkState : std::uint8_t { Connecting, SetupNeeded, Lost };

/// Everything the scene depends on at one instant.
struct SceneInput {
    const HostState* host = nullptr;         ///< nullptr while there is no live link
    std::uint32_t msSinceFrame = 0;          ///< time elapsed since `host` was received
    bool blinkOn = false;                    ///< global blink phase
    std::size_t page = 0;                    ///< page to show (see Pager)
    LinkState link = LinkState::Connecting;  ///< what to show while `host` is nullptr
    const Agent* detail = nullptr;           ///< when set, the screen shows this agent's detail view
    std::int8_t calibrationStep = -1;        ///< 0 or 1 while the guided calibration is open
};

/// Pure function: same input, same scene. Ages and the clock advance locally
/// between frames using `msSinceFrame`.
Scene buildScene(const SceneInput& input, const ScreenGeometry& geometry);

/// How many touches the guided calibration asks for.
inline constexpr std::size_t kCalibrationSteps = 2;

/// Where the calibration's `step`-th target is drawn: a diagonal across the
/// screen, kept off-square on purpose, so the axis pairing of the calibration
/// is unambiguous (ADR-0014).
TouchPoint calibrationTarget(std::size_t step, const ScreenGeometry& geometry);

/// The card containing the point, or -1. Only cards answer: the header, the
/// page dots, the +N badge, the gaps and the margins are not cards, and a
/// long press there does nothing.
std::int8_t cardAt(const Scene& scene, std::int16_t x, std::int16_t y);

/// Error cards and the LED blink only during the first minute of an error:
/// long enough to be noticed, short of flashing for hours.
inline constexpr std::uint32_t kBlinkWindowSeconds = 60;

/// Seconds since the agent's status changed, `elapsedSeconds` after its frame
/// arrived. Saturates instead of wrapping.
std::uint32_t ageAt(const Agent& agent, std::uint32_t elapsedSeconds);

/// True while `agent` is in an error that is still young enough to blink.
bool blinks(const Agent& agent, std::uint32_t elapsedSeconds);

}  // namespace managents::core
