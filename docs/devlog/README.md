# Devlog

Historical working notes from building the Go implementation, kept for the
learning narrative. These were written as milestone briefs *during*
development — they reference a private PRD, contain done-criteria checklists
frozen at the time of writing, and describe intermediate states of the code
that later milestones changed. **They are not documentation for the current
code**; for that, see each implementation's README.

- [clerk-migration.md](clerk-migration.md) — swapping WorkOS AuthKit for Clerk at the auth boundary
- [m3-server-auth.md](m3-server-auth.md) — validating Clerk JWTs on the Go WebSocket upgrade
- [m4-presence.md](m4-presence.md) — room presence and typing indicators in the room actor
