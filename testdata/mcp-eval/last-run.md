Model: sonnet. Date: 2026-10-15. One run per task.

| Task | Area | OK | Calls | Failed calls | Turns | Tokens | Cost, USD |
|---|---|---|---|---|---|---|---|
| setup-from-scratch | sessions+config | yes | 5 | 0 | 6 | 216446 | 0.065 |
| run-and-report-cost | sessions+metrics | yes | 2 | 0 | 3 | 79168 | 0.119 |
| answer-question | interaction | yes | 2 | 0 | 3 | 79286 | 0.030 |
| approve-one-permission | interaction | yes | 3 | 0 | 4 | 107113 | 0.036 |
| switch-model-and-restart | sessions | yes | 3 | 0 | 4 | 106392 | 0.037 |
| stop-project | sessions | yes | 1 | 0 | 2 | 70701 | 0.025 |
| update-session-config | projects+config | yes | 2 | 0 | 3 | 107223 | 0.037 |
| remove-project | projects+config | yes | 1 | 0 | 2 | 70795 | 0.025 |
| find-failed-session | logs | yes | 2 | 0 | 3 | 55211 | 0.086 |
| project-cost | metrics | yes | 1 | 0 | 2 | 35186 | 0.019 |
| generate-and-approve-roadmap | planning | yes | 3 | 0 | 4 | 146522 | 0.051 |
| skill-candidates | skills | yes | 1 | 0 | 2 | 35300 | 0.019 |
| wait-for-permission | events | yes | 2 | 0 | 3 | 79656 | 0.031 |

Success: 13/13 (100%). Failed calls per task: 0.00. Calls per task: 2.2. Tokens per task: 91461. Cost: $0.58.
