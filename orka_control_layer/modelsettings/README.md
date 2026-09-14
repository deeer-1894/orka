# Per-user model settings

Authenticated endpoints, beneath `/api/v1/controller`, use the existing
`{code, msg, data}` envelope:

- `POST /model-settings/get`: no payload fields required; returns
  `{provider, base_url, api_key_set, models: string[], enabled}`.
- `POST /model-settings/save`: accepts the same fields plus optional `api_key`;
  returns the redacted configuration. Blank/omitted keys preserve the saved key
  only when the normalized base URL is unchanged. A URL change clears the old
  key unless a new key is supplied. A payload of `{enabled:false}` also works.
- `POST /model-settings/discover`: accepts `{provider, base_url, api_key?}` and
  returns `{models:string[], source:"remote"|"preset", notice:string}`.
  Discovery never saves or enables settings.
- `GET /models`: returns Auto first (`version:"auto", label:"Auto",
  hint:"使用列表中的第一个模型"`), then each model with `version` equal to its name.

Provider is descriptive; every provider uses the OpenAI-compatible protocol.
Supply the full API base (including `/v1` or the provider's equivalent prefix).
Local/private hosts are allowed intentionally. Discovery appends `/models`,
refuses redirects, times out after 10 seconds, and reads at most 1 MiB. Failed
requests return a nonzero error envelope without the provider response body.
Only HTTP 404/405 from the normalized exact HTTPS bases
`https://ark.cn-beijing.volces.com/api/plan/v3` and
`https://ark.cn-beijing.volces.com/api/coding/v3` return `source:"preset"`.
Alternate hosts, explicit ports, escaped paths and other path prefixes do not
qualify. Discovery still requests the supplied base's `/models` first; it never
probes a second endpoint or switches subscription/billing routes. HTTP 401, 403,
429, redirects, malformed responses and network failures remain errors. The
preset notice explicitly says credentials, entitlements and model availability
have not been verified. Successful remote discovery returns `source:"remote"`
and an empty notice, including when the provider returns an empty list.
`DiscoverWithMetadata` exposes this distinction; `Discover` retains the old
list-only interface for existing callers.

The preset catalog was checked on **2026-09-14** against the official
[Coding Plan catalog](https://www.volcengine.com/docs/82379/1928261)
(updated 2026-09-08), [Agent Plan catalog](https://www.volcengine.com/docs/82379/2366394)
(updated 2026-09-14), and [console alias documentation](https://www.volcengine.com/docs/82379/2373738).
It starts with `doubao-seed-2.1-turbo`, followed by `doubao-seed-evolving`,
`doubao-seed-2.0-lite`, `minimax-m3`, `glm-5.3`, `glm-latest`, `glm-5.3-flash`,
`deepseek-v4-flash`, `deepseek-v4-pro`, `kimi-k2.7-code`, `kimi-k3`, and
`ark-code-latest` (the console-selected alias). Presets are not a verified list
of models available to the current account.

Manually saving a model does not require successful discovery. Enabled settings
require a nonempty model list. Auto uses its first entry; a manual selection stays
on that exact model throughout the run. Unknown explicit selections fail before
any provider call. Disabling the override restores deployment defaults.

Legacy private profiles prepend `model` to `models` and deduplicate in order.
The old `mini_model` adds no choice; if explicitly listed it remains an ordinary
model name. Public responses and newly saved files omit both legacy fields.
Old imports remain accepted. Deployment configuration follows the same ordering;
internal compatibility aliases cannot activate routing or role-based switching.

Files live at
`filepath.Join(filepath.Dir(baseStorage), "model-settings", sha256(owner)+".json")`,
with directory mode `0700`, file mode `0600`, and atomic replacement. This is
outside the storage tree mounted into the tools container. Missing settings use
deployment defaults; unreadable/corrupt settings fail closed rather than silently
using a different provider. Never relocate this directory into an agent workspace.

Each chat run captures a model/client snapshot in context. Delegates,
summaries, attachments, titles, digests, and recovery execution use that snapshot;
settings updates affect subsequent runs. The ChatService locks, cancellation map,
and confirmation registry stay on the original service instance. Keys are not
serialized into run requests or checkpoints. Recovery after a pause/restart takes
a fresh snapshot of the owner's current settings. The separately deployed Python
GUI planner retains its own environment-based model configuration.

Independent `/chat/followups` requests accept `selected_version` and `model_profile`
alongside `prompt` and `answer`. The frontend passes the last answer's
`meta.model_version` and `meta.model_profile`. The profile is a SHA-256 digest of
owner and private configuration, including the credential without exposing it.
Missing or changed profiles return empty suggestions without provider traffic.
Matching profiles still validate the selection; unknown names return an error.

User-provider clients share a limiter per owner/base URL/credential, even across
runs or model-list changes. The pool is capped at 128 connection identities and
expires idle clients after five minutes. Active and queued calls pin their entry;
when all entries are active, admission fails instead of bypassing the limit.
Snapshots reacquire that shared entry for each call, so eviction cannot split
concurrency control between an older snapshot and a newer request.
