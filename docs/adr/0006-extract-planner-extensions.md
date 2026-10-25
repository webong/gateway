---
status: accepted
---

# Extract product behavior into self-contained Gateway Extensions

Planner remains Gateway's generic CMS kernel, while Web, DNS, Email, and Database behavior lives in the Gateway Extensions `webong/web`, `webong/dns`, `webong/email`, and `webong/database`. Each Extension retains one identity and version across planes. Its Planner entrypoint owns that product area's resource kinds, management routes, migrations, commands, and protocol policy; its Spinner entrypoint owns the corresponding network runtime. Planner discovers its entrypoint through Gateway's extension-provider contract, while Spinner composes the Go runtime without making the shared contract depend on Spinner core. Arranger owns that language-neutral contract. The full Gateway host enables all four, while separately assembled Planner and Spinner hosts may enable only the Extensions they need.
