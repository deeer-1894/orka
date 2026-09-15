export interface AcceptanceResult {
  id: string;
  description: string;
  method: string;
  file?: string;
  status: 'passed' | 'failed' | 'unverified';
  actual?: unknown;
  expected?: unknown;
  sha256?: string;
  detail?: string;
}
export interface RunAcceptance {
  contract: { run_id: string; requests: unknown[]; truncated: boolean };
  checks: {
    at: string;
    spec_path: string;
    report: { ok: boolean; scope: string; error?: string; results: AcceptanceResult[] };
  }[];
}
export interface DeliverySnapshot {
  version: number;
  conversation_id: string;
  run_id: string;
  created_at: string | number;
  files: { path: string; size: number; sha256: string }[];
}
