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
  executions?: {revision_scope?: string; error?: string; files?: {path:string;sha256:string}[]; changed_during?: string[]; changed_since?: string[]; revisions_partial?: boolean; id: string; run_id: string; at: number; tool: string; command: string; ok: boolean; exit_code: number; timed_out: boolean; canceled: boolean; stdout: string; stderr: string; output_truncated: boolean}[];
  executions_truncated?: boolean;
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
