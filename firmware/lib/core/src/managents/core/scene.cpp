#include "managents/core/scene.hpp"

#include <cstdio>

#include "managents/core/format.hpp"

namespace managents::core {
namespace {

ContextView buildContextView(const ContextUsage& usage) {
    ContextView view;
    view.visible = usage.known && usage.limit > 0;
    view.percent = view.visible ? percentOf(usage.used, usage.limit) : 0;
    return view;
}

CardView buildCard(const Agent& agent, const Rect& bounds, std::uint32_t elapsedSeconds, bool blinkOn) {
    CardView card;
    card.bounds = bounds;
    card.kind = agent.kind;
    card.status = agent.status;
    card.alertPhase = blinks(agent, elapsedSeconds) && !blinkOn;
    char name[decltype(card.name)::capacity() + 1];
    foldToAscii(agent.name.c_str(), name, sizeof name);  // the fonts only draw ASCII
    card.name.assign(name);
    char age[decltype(card.age)::capacity() + 1];
    formatAge(ageAt(agent, elapsedSeconds), age, sizeof age);
    card.age.assign(age);
    card.context = buildContextView(agent.context);
    return card;
}

HeaderView buildHeader(const HostState& host, std::uint32_t elapsedSeconds, const ScreenGeometry& geometry) {
    HeaderView header;
    header.bounds = {0, 0, geometry.width, geometry.headerHeight};
    char text[8];
    formatClock(host.hostEpochSeconds + host.utcOffsetSeconds + elapsedSeconds, text, sizeof text);
    header.clock.assign(text);
    if (host.moreCount > 0) {
        std::snprintf(text, sizeof text, "+%u", static_cast<unsigned>(host.moreCount));
        header.overflowBadge.assign(text);
    }
    header.pageCount = static_cast<std::uint8_t>(Pager::pageCount(host.agentCount));
    for (std::size_t page = 0; page < header.pageCount; ++page) {
        const std::size_t first = page * Pager::kPerPage;
        const std::size_t remaining = host.agentCount - first;
        header.pageAttention[page] =
            summarize(host.agents + first, remaining < Pager::kPerPage ? remaining : Pager::kPerPage);
    }
    return header;
}

Rect gridArea(const ScreenGeometry& geometry) {
    const std::int16_t top = geometry.headerHeight;
    return {geometry.margin, top, static_cast<std::int16_t>(geometry.width - 2 * geometry.margin),
            static_cast<std::int16_t>(geometry.height - top - geometry.margin)};
}

DetailView buildDetail(const Agent& agent, const HostState& host, std::uint32_t elapsedSeconds,
                       const ScreenGeometry& geometry) {
    DetailView view;
    const std::int16_t top = geometry.headerHeight;
    view.bounds = {geometry.margin, top, static_cast<std::int16_t>(geometry.width - 2 * geometry.margin),
                   static_cast<std::int16_t>(geometry.height - top - geometry.margin)};
    view.kind = agent.kind;
    view.status = agent.status;
    char name[decltype(view.name)::capacity() + 1];
    foldToAscii(agent.name.c_str(), name, sizeof name);  // the fonts only draw ASCII
    view.name.assign(name);
    char age[decltype(view.age)::capacity() + 1];
    formatAge(ageAt(agent, elapsedSeconds), age, sizeof age);
    view.age.assign(age);
    // The clock time this status began: the frame's now, minus the age. The
    // protocol carries no session start, so the status is what can be shown.
    char started[decltype(view.started)::capacity() + 1];
    formatClock(host.hostEpochSeconds + host.utcOffsetSeconds - static_cast<std::int64_t>(ageAt(agent, elapsedSeconds)),
                started, sizeof started);
    view.started.assign(started);
    view.context = buildContextView(agent.context);
    return view;
}

SceneKind linkScene(LinkState link) {
    switch (link) {
        case LinkState::SetupNeeded:
            return SceneKind::SetupNeeded;
        case LinkState::Lost:
            return SceneKind::Reconnecting;
        case LinkState::Connecting:
            break;
    }
    return SceneKind::Connecting;
}

}  // namespace

Scene buildScene(const SceneInput& input, const ScreenGeometry& geometry) {
    Scene scene;
    // The calibration outranks everything: it is what the finger is doing now.
    if (input.calibrationStep >= 0) {
        const auto step = static_cast<std::size_t>(input.calibrationStep);
        scene.kind = SceneKind::Calibration;
        scene.calibration.step = static_cast<std::uint8_t>(step < kCalibrationSteps ? step : kCalibrationSteps - 1);
        scene.calibration.target = calibrationTarget(scene.calibration.step, geometry);
        return scene;
    }
    if (input.host == nullptr) {
        scene.kind = linkScene(input.link);
        scene.header.bounds = {0, 0, geometry.width, geometry.headerHeight};
        return scene;
    }

    const HostState& host = *input.host;
    const std::uint32_t elapsedSeconds = input.msSinceFrame / 1000;
    if (input.detail != nullptr) {
        scene.kind = SceneKind::Detail;
        scene.detail = buildDetail(*input.detail, host, elapsedSeconds, geometry);
        return scene;
    }
    scene.header = buildHeader(host, elapsedSeconds, geometry);
    if (host.agentCount == 0) {
        scene.kind = SceneKind::NoAgents;
        return scene;
    }

    const std::size_t pages = scene.header.pageCount;
    const std::size_t page = input.page < pages ? input.page : pages - 1;
    const std::size_t first = page * Pager::kPerPage;
    const std::size_t remaining = host.agentCount - first;
    const std::size_t count = remaining < Pager::kPerPage ? remaining : Pager::kPerPage;
    scene.header.page = static_cast<std::uint8_t>(page);

    scene.kind = SceneKind::Agents;
    scene.cardCount = static_cast<std::uint8_t>(count);
    Rect bounds[Pager::kPerPage];
    layoutGrid(count, gridArea(geometry), geometry.gap, bounds);
    for (std::size_t i = 0; i < count; ++i) {
        scene.cards[i] = buildCard(host.agents[first + i], bounds[i], elapsedSeconds, input.blinkOn);
    }
    return scene;
}

std::uint32_t ageAt(const Agent& agent, std::uint32_t elapsedSeconds) {
    const std::uint64_t age = static_cast<std::uint64_t>(agent.ageSeconds) + elapsedSeconds;
    return age > UINT32_MAX ? UINT32_MAX : static_cast<std::uint32_t>(age);
}

bool blinks(const Agent& agent, std::uint32_t elapsedSeconds) {
    return agent.status == AgentStatus::Error && ageAt(agent, elapsedSeconds) < kBlinkWindowSeconds;
}

TouchPoint calibrationTarget(std::size_t step, const ScreenGeometry& geometry) {
    // One near the top-left quarter, one near the bottom-right: the diagonal
    // spans both axes far enough for two touches to give a scale and an
    // offset each. 15 % and 20 % in from the edges keep both inside the
    // panel's touch view area.
    static constexpr std::int32_t kLeftPercent = 15;
    static constexpr std::int32_t kTopPercent = 20;
    const bool first = step % kCalibrationSteps == 0;
    const TouchPoint target{
        static_cast<std::int16_t>((geometry.width * (first ? kLeftPercent : 100 - kLeftPercent)) / 100),
        static_cast<std::int16_t>((geometry.height * (first ? kTopPercent : 100 - kTopPercent)) / 100),
    };
    return target;
}

std::int8_t cardAt(const Scene& scene, std::int16_t x, std::int16_t y) {
    for (std::uint8_t i = 0; i < scene.cardCount; ++i) {
        const Rect& bounds = scene.cards[i].bounds;
        if (x >= bounds.x && x < bounds.x + bounds.w && y >= bounds.y && y < bounds.y + bounds.h) {
            return static_cast<std::int8_t>(i);
        }
    }
    return -1;
}

}  // namespace managents::core
