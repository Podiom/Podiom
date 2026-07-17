import type { Goal } from "./types";

export interface GoalGroupEntry<T> {
  kind: "group";
  goalId: string;
  goal: Goal | null;
  label: string;
  items: T[];
}

export interface StandaloneEntry<T> {
  kind: "item";
  item: T;
}

export type GoalGroupedEntry<T> = GoalGroupEntry<T> | StandaloneEntry<T>;

export function goalLookup(goals: Goal[]): Map<string, Goal> {
  return new Map(goals.map((g) => [g.ID, g]));
}

export function goalLabel(goalId: string, goal: Goal | null | undefined): string {
  if (goal?.Title) return goal.Title;
  const short = goalId.length > 8 ? goalId.slice(0, 8) : goalId;
  return short ? `Goal ${short}` : "Goal";
}

export function goalGroupedEntries<T>(
  items: T[],
  getGoalId: (item: T) => string | undefined | null,
  goals: Goal[],
): GoalGroupedEntry<T>[] {
  const byGoal = goalLookup(goals);
  const groups = new Map<string, GoalGroupEntry<T>>();
  const entries: GoalGroupedEntry<T>[] = [];

  for (const item of items) {
    const goalId = (getGoalId(item) ?? "").trim();
    if (!goalId) {
      entries.push({ kind: "item", item });
      continue;
    }

    let group = groups.get(goalId);
    if (!group) {
      const goal = byGoal.get(goalId) ?? null;
      group = {
        kind: "group",
        goalId,
        goal,
        label: goalLabel(goalId, goal),
        items: [],
      };
      groups.set(goalId, group);
      entries.push(group);
    }
    group.items.push(item);
  }

  return entries;
}

export function goalGroupOpen(open: Record<string, boolean>, key: string): boolean {
  return open[key] ?? true;
}
