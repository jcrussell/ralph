# Revert state (post-mortem)

The orchestrator just auto-reverted after 3 consecutive dirty iterations. Your job in this single iteration: explain why and prevent recurrence.

1. `bd memories` — look for prior `avoid-*` entries on related topics.
2. Read the recent iteration logs in `.ralph/state/logs/summary.jsonl` to understand what the previous sessions were trying to do.
3. `bd defer <id> --reason "..."` for any task that was stuck.
4. `bd remember --key avoid-<topic> "..."` capturing the failure mode so future sessions don't repeat it. Be specific about root cause, not symptoms.
5. Do NOT attempt new work. Exit cleanly.
