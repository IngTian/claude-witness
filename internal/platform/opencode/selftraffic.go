package opencode

import (
	"context"
	"database/sql"
)

// MarkerName is the single identifier witness stamps on its OWN distillation
// sessions in OpenCode, so they are never re-ingested as user sessions. It is set
// as BOTH the session's agent and (legacy) title at creation — see server.go
// createSession. This one const replaces the three former declarations
// (witnessDistillTitle, openCodeAgentName, and a bare literal).
const MarkerName = "witness-distill"

// selfTrafficWhere builds the SQL predicate that matches witness's own distill
// sessions. AGENT is authoritative: witness sets agent=MarkerName at creation and
// OpenCode never rewrites it, whereas OpenCode's auto-titler CAN overwrite a
// session's title after the fact. So when the session table has an `agent` column
// we key on it alone; the title match is a fallback ONLY for older OpenCode schemas
// with no agent column.
//
// cleanup uses selfTrafficWhere directly (DELETE these); import uses
// selfTrafficExclude (skip these). Both build from the same column choice so they
// can never disagree on WHICH column is authoritative. hasAgent is resolved once
// per query from the live schema, keeping these pure/testable.
func selfTrafficWhere(hasAgent bool) (clause string, args []any) {
	if hasAgent {
		return `agent = ?`, []any{MarkerName}
	}
	return `title = ?`, []any{MarkerName}
}

// selfTrafficExclude builds the NULL-SAFE negation of selfTrafficWhere for the
// import filter. It is NOT `NOT (agent = ?)`: under SQL three-valued logic a NULL
// agent makes `agent = ?` NULL and `NOT NULL` NULL (falsy), which would SILENTLY
// DROP every genuine user session with a NULL agent — a permanent data-capture loss
// on the first full backfill (pre-agent-column sessions carry NULL after ADD
// COLUMN). SQLite's `IS NOT` compares NULL-safely, so a NULL agent correctly counts
// as "not witness's own" and is imported. Same column choice as selfTrafficWhere,
// so read/delete stay in agreement on the authoritative column.
func selfTrafficExclude(hasAgent bool) (clause string, args []any) {
	if hasAgent {
		return `agent IS NOT ?`, []any{MarkerName}
	}
	return `title IS NOT ?`, []any{MarkerName}
}

// sessionHasAgentColumn reports whether the OpenCode session table has an `agent`
// column, on a plain *sql.DB (the import path) rather than a tx. Mirrors
// hasSessionColumn, which operates on a *sql.Tx (the cleanup path).
func sessionHasAgentColumn(ctx context.Context, db *sql.DB) bool {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(session)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if name == "agent" {
			return true
		}
	}
	return false
}
