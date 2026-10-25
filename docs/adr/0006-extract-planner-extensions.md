---
status: accepted
---

# Extract product behavior into Gateway Extensions

Planner remains Gateway's generic CMS kernel, while Web, DNS, Email, and Database behavior lives in the Gateway Extensions `webong/web`, `webong/dns`, `webong/email`, and `webong/database`. Each Extension retains one identity and version across planes; this extraction creates its Planner-side entrypoint, which owns that product area's resource kinds, management routes, migrations, commands, and protocol policy. The entrypoint registers through Gateway's extension-provider contract instead of being hard-coded in Planner. The full Gateway host enables all four, while a separately assembled Planner may enable only the Extensions it needs.
