# Research tools and long runs

Orka keeps network retrieval, run memory, and run accounting separate. No new external search subscription, crawler service, or vector database is required.

## Document tools

- `discover_docs(url, query?, limit?)` probes `llms.txt` and `sitemap.xml` in the supplied documentation directory, then its supplied landing page, before falling back to host-root indexes. Explicit Markdown/HTML/XML index files are read directly. It expands recognized nested `llms.txt` or `/_llms/*.md`/`.txt` catalogs and sitemap children by at most one level. A supplied HTML landing page with one unique same-origin directory link (a path ending in `/`, with an optional query string) may also expand once; ordinary document links are not crawled.

  Discovery takes its preferred language from a recognized code in the input URL path, defaulting to English. If unavailable, expansion falls back to English, then unlocalized catalogs, then a deterministic alternative. Regional codes use their base language. Within a language, shallower catalogs precede version branches, with URL order breaking ties. Only the preferred available language's catalogs are expanded. The JSON output retains `index_source` and `results`; each result has `title`, `url`, and `kind`. `kind: "page"` results rank before unexpanded `kind: "index"` leads, then by query relevance and language preference. An index lead is navigation, not article evidence; an expanded catalog is replaced by its discovered links. Sitemap child references are traversal-only. The limit defaults to ten and permits 1–20 results.

  The entire call has a 20-second deadline and a shared five-HTTP-request budget, including redirects and failed probes. Each response is capped at 512 KiB, with at most 5,000 retained candidate links. Cross-origin links and redirects, cycles, and deeper recursive traversal are excluded. Unexpanded catalogs can remain when these bounds or language preferences limit discovery. JavaScript is not executed.
- `read_section(url, query, max_chars?)` downloads at most 2 MiB and returns the relevant readable passages with source metadata. Source headers are additional to this excerpt budget. The excerpt defaults to 4,000 Unicode characters; `max_chars` permits 1–20,000. HTML extraction is heuristic. For dynamic pages use the existing browser capability.
- `fetch_url` remains the general readable-page tool with its existing 20,000-byte preview limit. Documentation needing a passage beyond that preview should use `read_section`.
- `search_evidence(query?, limit?)` searches this execution's saved retrieval results by keywords, source URL or evidence ID. It returns source identity, retrieval time, a short matching excerpt, and a file path when persistence succeeded. Empty query lists the first records; limit defaults to five and is capped at ten.

`discover_docs` and `read_section` use the existing `web:search` scope. The control layer also makes the new tools available to the built-in researcher. Existing confirmation rules for execution and writes are unchanged.

## Run-scoped evidence

The Eino adapter delegates retrieval to one research session shared by the main agent and its delegates. Identical canonical requests share one in-flight call and a cached result for the lifetime of that execution. Failed or unavailable responses are not saved as successful evidence. Cache hits are labeled and carry the original retrieval time; use a new execution for a fresh snapshot.

Successful tool observations are saved under `.orka_offload/<run-id>/evidence/` in the user's workspace, alongside immutable catalog snapshots. An execution without a database run ID gets its own unique directory. No cache is shared across users or executions. If a write fails, the result is returned without a fictitious file path and without shortening the only accessible copy.

The evidence store saves readable tool output, not necessarily the full remote page: a preview or a selected section is labeled by its originating tool. It is a source inventory, not a guarantee that the page supports every claim; final reports must still connect claims to actual evidence.

## Retrieval allowance

`agent.research_max_calls` in `config.yaml` or `RESEARCH_MAX_CALLS` sets the maximum logical external research calls per execution. Non-positive values use the default of 40. Concurrent duplicates and cache hits do not consume another call; failures do consume a call so repeated failures cannot run forever. A tool/provider can perform bounded internal retries or index reads within one logical call.

New calls to `web_search`, `fetch_url`, `discover_docs` and `read_section` stop once this allowance is reached or half of the run's token budget has been spent. Local evidence reads and delivery tools remain usable. This is a convergence policy for those research tools, not a network sandbox: generic HTTP and execution tools retain their existing permissions.

A short live reminder after context summarization lists the current evidence catalog and unfinished plan steps. It asks the agent to save sourced findings, stop research when evidence is sufficient, and reserve time for producing and verifying deliverables. The reminder never marks a step complete. The existing overall budget still determines a partial terminal status.

## Accounting and verification

Final run tokens come from the same meter as the overall budget, including auxiliary summaries, attachment prepasses and failovers. A reported zero differs from unavailable metering; legacy paths without a usage report fall back to agent-event counts. The default configured model is recorded when no explicit selection was made.

Regression tests use local HTTP fixtures, the real Eino runner with scripted model responses, and the production `ChatService.Run` entry. They cover source metadata, relevant passages beyond preview limits, malformed responses, user/run isolation, concurrent deduplication, cancellation, evidence lookup, immutable catalogs, degraded persistence paths, budget reservation and auxiliary usage. Live comparisons are recorded separately under `docs/evaluations/`.

## Repeated local evidence reads

Catalog snapshots contain compact source metadata without repeating body previews. Reading a saved source file still executes the existing file tool and its permission checks. When an identical source body is read again in the same agent execution, the model receives a short receipt and preview pointing it to targeted evidence search. Every agent’s first full read, edited source files, failures and reads of ordinary delivery files retain their normal behavior. Full source files remain available; this reduces repeated context expansion rather than blocking filesystem access.

Tool visibility is scoped to each agent invocation’s registered catalog. Tool activation is shared within the run only where each agent actually has that tool registered. Specialist delegates receive the shared evidence/allowance reminder while keeping their own final budget cutoff.
