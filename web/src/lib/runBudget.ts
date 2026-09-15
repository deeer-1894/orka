export interface RunBudgetLimits { max_tokens?: number; max_wall_seconds?: number; max_steps?: number }
export interface BudgetUsage { used_tokens: number; reserved_tokens: number; unknown_calls: number; unknown_tokens: number; estimated_tokens: number }
export interface RunBudget extends BudgetUsage {
  run_id: string;
  budget_run_id: string;
  status: string;
  limits: RunBudgetLimits;
  remaining_tokens: number;
  used_steps: number;
  deadline: string;
  sources: (BudgetUsage & { source: string })[];
}
export function validateBudget(budget: RunBudgetLimits) {
  if (Object.values(budget).some(value => value !== undefined && (!Number.isSafeInteger(value) || value < 0))) {
    throw new Error('预算须为非负整数，留空或 0 使用部署默认。');
  }
}
