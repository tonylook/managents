#include "managents/core/chime.hpp"

namespace managents::core {

ChimeDecision chimeFor(const HostState& previous, const HostState& current) {
    ChimeDecision decision;
    for (std::uint8_t i = 0; i < current.agentCount; ++i) {
        const Agent& agent = current.agents[i];
        if (agent.status != AgentStatus::Waiting && agent.status != AgentStatus::Error) {
            continue;
        }
        // Matching is by id, the session's stable name: a second session in
        // the same folder carries a different id, whatever its card says.
        const Agent* before = nullptr;
        for (std::uint8_t j = 0; j < previous.agentCount; ++j) {
            if (previous.agents[j].id == agent.id) {
                before = &previous.agents[j];
                break;
            }
        }
        if (before == nullptr || before->status == agent.status) {
            continue;
        }
        const Chime which = agent.status == AgentStatus::Error ? Chime::Error : Chime::Waiting;
        if (!decision.play || which == Chime::Error) {
            decision.play = true;
            decision.which = which;
        }
    }
    return decision;
}

}  // namespace managents::core
