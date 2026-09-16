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
