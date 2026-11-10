---
id: MEM-001
schema_version: 1
name: "GitStack"
status: active
relationship: reference
repository_path: "E:/Git/GitStack"
remote_url: "github.com/fathormb/gitstack"
created: "2026-11-10T18:07:41.137547300+01:00"
updated: "2026-11-10T23:30:01.652420400+01:00"
last_seen: "2026-11-10T23:30:01.652420400+01:00"
encounter_count: 2
tags: ["databases", "go", "hosting", "react"]
encounters:
  - timestamp: "2026-11-10T18:07:41.137547300+01:00"
    trigger: explicit
    actor: "lead"
    context: "Analysis phase of NidaOS, before the first milestone."
    commit: "fdc6bd2bc3da3652d60821d3086543202845f69e"
    branch: "main"
    dirty: false
    observation: "GitStack runs on k3s with Traefik, PostgreSQL and NATS. The `core` service contains two controllers that are not related to Git: `apps` (one namespace for each application, IngressRoute, login through the gateway) and `databases` (managed PostgreSQL as a StatefulSet with a local-path volume). An external runner uses a pull protocol and needs no inbound port."
    evidence:
      - path: "docs/adr/0002-architettura-hosting-app.md"
        note: "Design of application hosting: controller in core, namespaces, Traefik routes, login."
      - path: "docs/adr/0003-database-gestiti.md"
        note: "Design of managed PostgreSQL: StatefulSet, local-path volume, no operator."
      - path: "docs/ci-runner-esterno.md"
        note: "External runner with a pull protocol."
      - path: "README.md"
        note: "Stack and monorepo structure."
  - timestamp: "2026-11-10T23:30:01.652420400+01:00"
    trigger: explicit
    actor: "lead"
    context: "End of the first NidaOS session, after the operator selected Go and React for the reuse of GitStack parts."
    commit: "fdc6bd2bc3da3652d60821d3086543202845f69e"
    branch: "main"
    dirty: false
    observation: "GitStack is a Go workspace (go.work) with the services core, gateway, git and identity. The web interface is in `web/` and uses React 19 with Vite. The app controller and the database controller are in `services/core/internal`. GitStack manages only PostgreSQL, in versions 16 and 17."
    evidence:
      - path: "go.work"
        note: "The Go workspace of the services."
      - path: "web/package.json"
        note: "React and Vite are the base of the web interface."
      - path: "docs/adr/0003-database-gestiti.md"
        note: "Managed PostgreSQL: rules for access, backups, restore and updates."
---
# GitStack

## Summary

GitStack is the system that NidaOS replaces on the home server. Its hosting parts are a possible source for the NidaOS private cloud.

## Relevance

NidaOS milestones M-02, M-03, M-04 and M-06 reuse the Go controllers and parts of the React interface (decisions D4, D9, D10 and D13).
