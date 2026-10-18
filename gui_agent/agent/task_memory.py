"""Bounded model observations for one GUI task, separate from action receipts.

Checkpoints describe the screenshot BEFORE the accompanying action. They are
model claims, never proof of a browser effect or independently accepted work.
"""
from collections import OrderedDict

MAX_GOALS = 8
GOAL_LIMIT = 96
OBSERVATION_LIMIT = 1800
MEMORY_NOTE = (
    "Model-reported task progress, not acceptance evidence or operator receipts. "
    "Each observation refers to the pre-action screenshot/observation step. "
    "Retain earlier phase readouts when the page changes; verify claims against their source."
)


class TaskMemory:
    def __init__(self, evidence):
        self.evidence = evidence
        self.goals = OrderedDict()
        self.omitted = 0

    def record(self, update, source):
        if not isinstance(update, dict):
            return False
        goal, observation, status = (update.get(key) for key in ("goal", "observation", "status"))
        if not isinstance(goal, str) or not goal.strip() or not isinstance(observation, str):
            return False
        if status not in ("pending", "complete", "blocked") or (status != "pending" and not observation.strip()):
            return False
        goal = self.evidence.clean(goal.strip(), GOAL_LIMIT)
        observation = self.evidence.clean(observation, OBSERVATION_LIMIT)
        previous = self.goals.get(goal)
        if previous and previous["observation"] == observation:
            source = {key: previous[key] for key in ("observed_step", "observation_seq", "screenshot_sha256")}
        self.goals[goal] = {"goal": goal, "status": status,
            "observation": observation,
            "source": "model_observation", **source}
        self.goals.move_to_end(goal)
        if len(self.goals) > MAX_GOALS:
            self.goals.popitem(last=False)
            self.omitted += 1
        return True

    def snapshot(self):
        # Re-sanitize if a later input was classified as sensitive.
        goals = [{key: self.evidence.clean(value, OBSERVATION_LIMIT if key == "observation" else GOAL_LIMIT)
                  if key in ("goal", "observation") else value
                  for key, value in goal.items()} for goal in self.goals.values()]
        return {"goals": goals, "omitted_goals": self.omitted, "note": MEMORY_NOTE}
