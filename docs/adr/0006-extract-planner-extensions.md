---
status: accepted
---

# Extract product behavior into self-contained Gateway Extensions

Planner remains Gateway's generic CMS kernel, while Web, DNS, Email, and Database behavior lives in the Gateway Extensions `webong/web`, `webong/dns`, `webong/email`, and `webong/database`. Each Extension retains one identity and version across planes. Its Planner entrypoint owns that product area's resource kinds, management routes, migrations, commands, and protocol policy; its Spinner entrypoint owns the corresponding network runtime. Planner discovers its entrypoint through Gateway's extension-provider contract, while Spinner composes the Go runtime without making the shared contract depend on Spinner core. Arranger owns that language-neutral contract. The full Gateway host enables all four, while separately assembled Planner and Spinner hosts may enable only the Extensions they need.

Spinner hosts do not import first-party Extension implementations. A verified package declares a named process entrypoint and exact forward and reverse operations. Planner's generic Extension resource pins the artifact and carries a bounded, non-secret configuration object. Each child validates its own configuration, receives no Planner credential, and can call only host capabilities declared by its descriptor. Web and DNS use this process boundary; Email must move its durable adapters, private submission route, and listener lifecycle through the same contract before the old in-process composition is removed.
