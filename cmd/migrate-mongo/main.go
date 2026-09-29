// Command migrate-mongo is the one-shot Mongo→Postgres data migration for
// the taskman cutover (docs/DESIGN_PG_FSM_MIGRATION.md, "Data migration +
// cutover", wave 4.1).
//
// Default mode is a VALIDATING DRY RUN: it reads every collection, builds
// the full row plan, runs every validation, inspects the target inside a
// READ ONLY transaction and prints the report — it writes nothing anywhere.
//
// --apply: refuses on any FATAL finding; otherwise runs repo.Migrate on the
// target itself (so the cutover can't be done in the wrong order — the
// schema is the embedded one the server would apply), then writes
// everything in ONE transaction (all-or-nothing), then re-reads both sides
// and prints PASS/FAIL verification lines.
//
// It only ever reads Mongo. Sessions are not migrated (the team re-logs-in).
//
// Exit codes: 0 success; 1 findings refused the run, the transaction rolled
// back, or post-apply verification failed; 2 usage / connection errors.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"taskman/internal/repo"
)

type runOptions struct {
	MongoURI string
	MongoDB  string
	PGDSN    string
	Apply    bool
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	var o runOptions
	fs := flag.NewFlagSet("migrate-mongo", flag.ExitOnError)
	fs.StringVar(&o.MongoURI, "mongo-uri", envOr("MONGO_URI", "mongodb://localhost:27017"), "source Mongo URI (read-only use)")
	fs.StringVar(&o.MongoDB, "mongo-db", envOr("MONGO_DB", "taskman"), "source Mongo database")
	fs.StringVar(&o.PGDSN, "pg-dsn", "", "target Postgres DSN (required)")
	fs.BoolVar(&o.Apply, "apply", false, "perform the migration (default: validating dry run, writes nothing)")
	_ = fs.Parse(os.Args[1:])

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	os.Exit(run(ctx, o, os.Stdout))
}

func run(ctx context.Context, o runOptions, w io.Writer) int {
	if o.PGDSN == "" {
		fmt.Fprintln(w, "error: --pg-dsn is required")
		return 2
	}
	mode := "DRY RUN"
	if o.Apply {
		mode = "APPLY"
	}
	fmt.Fprintf(w, "taskman migrate-mongo — %s\n", mode)
	fmt.Fprintf(w, "  source: %s  db=%s\n", redact(o.MongoURI), o.MongoDB)
	fmt.Fprintf(w, "  target: %s\n", redact(o.PGDSN))

	// ---- read Mongo ----
	mc, err := connectMongo(ctx, o.MongoURI)
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 2
	}
	defer func() { _ = mc.Disconnect(context.Background()) }()
	mdb := mc.Database(o.MongoDB)
	src, err := readSource(ctx, mdb)
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 2
	}
	p := buildPlan(src)

	fmt.Fprintln(w, "\n== Mongo read ==")
	fmt.Fprintf(w, "  %-9s %6d  (embedded activity entries: %d)\n", "tasks", len(src.Tasks), p.EmbeddedActivity)
	fmt.Fprintf(w, "  %-9s %6d\n", "teams", len(src.Teams))
	fmt.Fprintf(w, "  %-9s %6d\n", "members", len(src.Members))
	fmt.Fprintf(w, "  %-9s %6d  (NOT migrated by design — team re-logs-in)\n", "sessions", src.Sessions)

	// ---- inspect target (read-only) ----
	db, err := repo.Open(o.PGDSN)
	if err != nil {
		fmt.Fprintf(w, "error: postgres open: %v\n", err)
		return 2
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}
	st, err := inspectTarget(ctx, db)
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 2
	}
	p.Findings = append(targetFindings(st), p.Findings...)
	fmt.Fprintln(w, "\n== Target ==")
	if st.Migrated {
		fmt.Fprintf(w, "  schema: migrated (version %d, dirty=%v)\n", st.Version, st.Dirty)
		fmt.Fprintf(w, "  existing rows: teams=%d members=%d member_teams=%d tasks=%d task_activity=%d sessions=%d\n",
			st.Counts["teams"], st.Counts["members"], st.Counts["member_teams"], st.Counts["tasks"], st.Counts["task_activity"], st.Counts["sessions"])
	} else {
		fmt.Fprintln(w, "  schema: not migrated (--apply runs repo.Migrate first)")
	}

	fmt.Fprintln(w, "\n== Planned rows ==")
	fmt.Fprintf(w, "  %-13s %6d\n", "teams", len(p.Teams))
	fmt.Fprintf(w, "  %-13s %6d\n", "members", len(p.Members))
	fmt.Fprintf(w, "  %-13s %6d\n", "member_teams", len(p.MemberTeams))
	fmt.Fprintf(w, "  %-13s %6d\n", "tasks", len(p.Tasks))
	fmt.Fprintf(w, "  %-13s %6d\n", "task_activity", p.ActivityRows)
	fmt.Fprintf(w, "  %-13s %6d\n", "sessions", 0)

	printFindings(w, p.Findings)
	fatals := 0
	for _, f := range p.Findings {
		if f.Sev == sevFatal {
			fatals++
		}
	}

	fmt.Fprintln(w, "\n== Result ==")
	if !o.Apply {
		if fatals > 0 {
			fmt.Fprintf(w, "DRY RUN — nothing written. %d FATAL finding(s): --apply would REFUSE.\n", fatals)
			return 1
		}
		fmt.Fprintln(w, "DRY RUN — nothing written. No fatal findings: --apply would proceed.")
		return 0
	}
	if fatals > 0 {
		fmt.Fprintf(w, "REFUSED — %d FATAL finding(s); nothing written. Fix the source data and re-run.\n", fatals)
		return 1
	}

	if err := repo.Migrate(o.PGDSN); err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 2
	}
	if err := applyPlan(ctx, db, p, true); err != nil {
		fmt.Fprintf(w, "APPLY FAILED — %v\n", err)
		return 1
	}
	fmt.Fprintf(w, "COMMITTED — teams=%d members=%d member_teams=%d tasks=%d task_activity=%d\n",
		len(p.Teams), len(p.Members), len(p.MemberTeams), len(p.Tasks), p.ActivityRows)

	fmt.Fprintln(w, "\n== Post-apply verification ==")
	if !verify(ctx, w, mdb, db, p) {
		fmt.Fprintln(w, "\n!!! VERIFICATION FAILED — the transaction is ALREADY COMMITTED. !!!")
		fmt.Fprintln(w, "!!! Do NOT start the server on Postgres. Roll back: keep running on the untouched Mongo")
		fmt.Fprintln(w, "!!! volume, and drop/recreate the Postgres schema before any retry.")
		return 1
	}
	fmt.Fprintln(w, "\nALL CHECKS PASSED.")
	return 0
}

func printFindings(w io.Writer, fs []finding) {
	fmt.Fprintln(w, "\n== Findings ==")
	if len(fs) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	for _, sev := range []severity{sevFatal, sevWarn, sevInfo} {
		var group []finding
		for _, f := range fs {
			if f.Sev == sev {
				group = append(group, f)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s (%d)\n", sev, len(group))
		for _, f := range group {
			if f.ID != "" {
				fmt.Fprintf(w, "  [%s %s] %s\n", f.Coll, f.ID, f.Msg)
			} else {
				fmt.Fprintf(w, "  [%s] %s\n", f.Coll, f.Msg)
			}
		}
	}
}

// redact hides a password in a URI/DSN for printing.
func redact(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
	}
	return u.String()
}
