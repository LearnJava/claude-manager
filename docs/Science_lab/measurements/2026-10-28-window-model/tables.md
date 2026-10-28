handoffs with both transcripts: 5 (skipped, previous transcript gone: 2)
| run | session | summary tok | reopen reads | reopen tok (chars/4) | tool calls before 1st write | prev calls | prev last ctx |
|---|---|---|---|---|---|---|---|
| 259 | ARB/Developer 1 | 735 | 0 | 0 | None | 108 | 203287 |
| 268 | ARB/Developer 1 | 561 | 0 | 0 | None | 42 | 104238 |
| 573 | Lumen browser/Chat | 470 | 0 | 0 | None | 55 | 110172 |
| 574 | Lumen browser/Chat | 519 | 0 | 0 | 15 | 4 | 35748 |
| 214 | Lumen browser/S6 | 519 | 0 | 0 | 21 | 80 | 134523 |
| **median** | | 519.25 | 0 | 0.0 | 18.0 | | |

growth (main transcripts, no handoff, >= 30 calls, last 30 days):
| model | sessions | calls | ctx0 median | new tok/call median (mean) | out tok/call median (mean) | 1h share of writes | median calls/session |
|---|---|---|---|---|---|---|---|
| opus | 85 | 5601 | 30625 | 1043 (2194) | 345 (676) | 100% | 53 |
| sonnet | 223 | 13801 | 30629 | 1074 (1910) | 300 (572) | 100% | 58 |

context at call k (median over sessions with >= k calls; n in brackets):
- opus: k=1: 30625 (n=85), k=10: 50797 (n=85), k=25: 81377 (n=85), k=50: 117886 (n=52), k=100: 178921 (n=13)
- sonnet: k=1: 30629 (n=223), k=10: 50253 (n=223), k=25: 76677 (n=223), k=50: 113890 (n=146), k=100: 153854 (n=15)

Prices ($/M): Opus 5.5 in 4.0, out 20.0, read 0.40; Sonnet 5.5 in 2.0, out 10.0, read 0.20 (PRICES x READ=0.1). H=150000, S=519; overhead per handoff: lower bound E=0, upper bound E=18 calls adding 19332 tokens. Token profile: Sonnet main sessions.

### base: median tokens/call, read = READ x input, handoff overhead = upper bound
| N productive calls | Sonnet, no cap $ | Sonnet + handoff $ (handoffs) | Opus 1M $ (peak ctx) | Opus/Sonnet+handoff at equal calls | break-even Opus/Sonnet call ratio |
|---|---|---|---|---|---|
| 25 | 0.52 | 0.52 (0) | 1.05 (57K) | 2.00 | 0.38 |
| 50 | 1.06 | 1.06 (0) | 2.11 (84K) | 2.00 | 0.51 |
| 75 | 1.73 | 1.73 (0) | 3.45 (111K) | 2.00 | 0.55 |
| 100 | 2.53 | 2.53 (0) | 5.06 (138K) | 2.00 | 0.58 |
| 150 | 4.54 | 4.20 (1) | 9.07 (192K) | 2.16 | 0.58 |
| 200 | 7.08 | 5.75 (1) | 14.16 (245K) | 2.46 | 0.55 |
| 300 | 13.78 | 9.32 (3) | 27.57 (353K) | 2.96 | 0.51 |
| 500 | 33.63 | 15.51 (5) | 67.26 (568K) | 4.34 | 0.42 |
| 800 | 79.51 | 24.90 (8) | 159.02 (890K) | 6.39 | 0.35 |

### sensitivity: handoff overhead = 0 (measured exact reopen)
| N productive calls | Sonnet, no cap $ | Sonnet + handoff $ (handoffs) | Opus 1M $ (peak ctx) | Opus/Sonnet+handoff at equal calls | break-even Opus/Sonnet call ratio |
|---|---|---|---|---|---|
| 25 | 0.52 | 0.52 (0) | 1.05 (57K) | 2.00 | 0.38 |
| 50 | 1.06 | 1.06 (0) | 2.11 (84K) | 2.00 | 0.51 |
| 75 | 1.73 | 1.73 (0) | 3.45 (111K) | 2.00 | 0.55 |
| 100 | 2.53 | 2.53 (0) | 5.06 (138K) | 2.00 | 0.58 |
| 150 | 4.54 | 3.77 (1) | 9.07 (192K) | 2.41 | 0.54 |
| 200 | 7.08 | 5.13 (1) | 14.16 (245K) | 2.76 | 0.51 |
| 300 | 13.78 | 7.75 (2) | 27.57 (353K) | 3.56 | 0.45 |
| 500 | 33.63 | 13.05 (4) | 67.26 (568K) | 5.15 | 0.38 |
| 800 | 79.51 | 21.17 (7) | 159.02 (890K) | 7.51 | 0.32 |

### sensitivity: mean tokens/call, overhead upper bound
| N productive calls | Sonnet, no cap $ | Sonnet + handoff $ (handoffs) | Opus 1M $ (peak ctx) | Opus/Sonnet+handoff at equal calls | break-even Opus/Sonnet call ratio |
|---|---|---|---|---|---|
| 25 | 0.72 | 0.72 (0) | 1.45 (78K) | 2.00 | 0.46 |
| 50 | 1.56 | 1.56 (0) | 3.13 (126K) | 2.00 | 0.53 |
| 75 | 2.64 | 2.87 (1) | 5.29 (174K) | 1.84 | 0.62 |
| 100 | 3.96 | 3.70 (1) | 7.92 (222K) | 2.14 | 0.57 |
| 150 | 7.31 | 5.83 (2) | 14.63 (317K) | 2.51 | 0.54 |
| 200 | 11.62 | 7.97 (3) | 23.24 (413K) | 2.92 | 0.50 |
| 300 | 23.10 | 12.25 (5) | 46.20 (604K) | 3.77 | 0.45 |
| 500 | 57.51 | 20.83 (9) | 115.02 (985K) | 5.52 | 0.38 |
| 800 | 137.77 | 33.75 (15) | 275.55 (1558K) | 8.16 | 0.31 |

### sensitivity: Opus read $0.20/M (as in the task text), overhead upper bound
| N productive calls | Sonnet, no cap $ | Sonnet + handoff $ (handoffs) | Opus 1M $ (peak ctx) | Opus/Sonnet+handoff at equal calls | break-even Opus/Sonnet call ratio |
|---|---|---|---|---|---|
| 25 | 0.52 | 0.52 (0) | 0.83 (57K) | 1.58 | 0.50 |
| 50 | 1.06 | 1.06 (0) | 1.54 (84K) | 1.46 | 0.67 |
| 75 | 1.73 | 1.73 (0) | 2.39 (111K) | 1.39 | 0.74 |
| 100 | 2.53 | 2.53 (0) | 3.38 (138K) | 1.34 | 0.78 |
| 150 | 4.54 | 4.20 (1) | 5.75 (192K) | 1.37 | 0.79 |
| 200 | 7.08 | 5.75 (1) | 8.66 (245K) | 1.51 | 0.75 |
| 300 | 13.78 | 9.32 (3) | 16.09 (353K) | 1.73 | 0.70 |
| 500 | 33.63 | 15.51 (5) | 37.40 (568K) | 2.41 | 0.59 |
| 800 | 79.51 | 24.90 (8) | 85.47 (890K) | 3.43 | 0.49 |