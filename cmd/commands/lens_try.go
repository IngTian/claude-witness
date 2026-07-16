package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/IngTian/witness/internal/distill"
	"github.com/IngTian/witness/internal/lens"
	"github.com/IngTian/witness/internal/store"
	"github.com/spf13/cobra"
)

// `witness lens try` flags. Package-level so the cobra command's RunE closure can
// bind them; one process per invocation, so shared state is fine.
var (
	trySessions int
	trySession  string
	tryJSON     bool
)

// newLensTryCmd builds the `try` subcommand. Unlike the other lens verbs (thin thunks
// into cmdLens), `try` carries its own flags, so it is a full command with a closure.
func newLensTryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "try <file>",
		Short: "Preview a lens's EXTRACT prompt on real sessions (read-only, writes nothing).",
		Long: "Mine real sessions from your archive through a CANDIDATE lens file and print the raw " +
			"observations it would produce — WITHOUT registering the lens or writing anything to the " +
			"archive. This is the prompt-tuning loop: edit the EXTRACT prompt, run `try`, see what " +
			"changes. Sessions are sampled largest-first (deterministic, and the meatiest sessions are " +
			"the ones a prompt is most likely to mishandle).\n\n" +
			"Claude-only for now: it runs lock-free because Claude's runner has no shutdown sweep. On an " +
			"OpenCode runner it refuses rather than risk disrupting a background worker.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmdLensTry(args[0], trySessions, trySession, tryJSON)
		},
	}
	c.Flags().IntVar(&trySessions, "sessions", 3, "number of sessions to sample (largest-first)")
	c.Flags().StringVar(&trySession, "session", "", "preview one specific session id (bypasses sampling)")
	c.Flags().BoolVarP(&tryJSON, "json", "j", false, "output as JSON")
	return c
}

// --- JSON output shape (stable, for diffing v1-vs-v2 prompt runs) -------------

type lensTryObsJSON struct {
	Dimension   string `json:"dimension"`
	Observation string `json:"observation"`
	Evidence    string `json:"evidence"`
	Poignancy   int    `json:"poignancy"`
}

type lensTrySessionJSON struct {
	Session      string           `json:"session"`
	RawTurns     int              `json:"raw_turns"`
	RawBytes     int64            `json:"raw_bytes"`
	ChunkCount   int              `json:"chunk_count"`
	Drifted      bool             `json:"drifted"`
	ElapsedMS    int64            `json:"elapsed_ms"`
	Observations []lensTryObsJSON `json:"observations"`
}

type lensTryJSON struct {
	Lens       string               `json:"lens"`
	Model      string               `json:"model"`
	Candidate  bool                 `json:"candidate"` // true = shown name is a fallback (file's # name: was reserved)
	Sessions   []lensTrySessionJSON `json:"sessions"`
	TotalObs   int                  `json:"total_observations"`
	DriftedAny bool                 `json:"drifted_any"`
}

// cmdLensTry runs the read-only preview. It opens its OWN store (independent of cmdLens)
// and, crucially, takes NO WorkerLock: on Claude the runner's Close is a no-op (no
// global sweep), so a preview can safely overlap a background worker.
func cmdLensTry(file string, nSessions int, oneSession string, asJSON bool) error {
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()

	// Load the candidate. Strict first; on a reserved-name collision fall back to the
	// lenient loader (preview never writes, so an impersonating name is harmless) and
	// mark it so the display makes the fallback obvious.
	candidate := false
	ln, err := lens.LoadFromFile(file)
	if err != nil {
		var uErr error
		if ln, uErr = lens.LoadFromFileUnchecked(file); uErr != nil {
			return uErr // a genuine load error (missing file / no EXTRACT) — surface it
		}
		// LoadFromFileUnchecked succeeded where LoadFromFile failed => reserved name.
		ln.Name = "candidate"
		candidate = true
	}

	// Slice 1 is Claude-only. OpenCode's Close() runs a process-global cleanup sweep
	// that could delete a concurrent worker's in-flight distill session; guarding that
	// safely needs the WorkerLock (a later slice). Refuse loudly rather than risk it.
	cfg := st.LoadConfig()
	cfg.Runner = st.ResolveRunner(cfg)
	if cfg.Runner != store.RunnerClaude {
		return fmt.Errorf("`lens try` currently supports only the claude runner (resolved runner is %q); "+
			"set it with `witness config set runner claude`, or run a full `witness lens rebuild` under your runner", cfg.Runner)
	}

	// Resolve the session set.
	var sessions []string
	if oneSession != "" {
		if st.RawCount(oneSession) == 0 {
			return fmt.Errorf("session %q has no raw turns (nothing to preview)", oneSession)
		}
		sessions = []string{oneSession}
	} else {
		if nSessions < 1 {
			nSessions = 1
		}
		sessions, err = st.SampleSessions(nSessions)
		if err != nil {
			return fmt.Errorf("sample sessions: %w", err)
		}
		if len(sessions) == 0 {
			return fmt.Errorf("archive has no sessions to preview")
		}
	}

	ctx := context.Background()
	runner, runFn, err := openRunner(ctx, st, cfg)
	if err != nil {
		return err
	}
	defer runner.Close() // no-op on Claude; still correct to call

	if asJSON {
		return lensTryEmitJSON(ctx, st, cfg, runFn, ln, candidate, sessions)
	}
	return lensTryRenderHuman(ctx, st, cfg, runFn, ln, candidate, sessions)
}

// modelLabel renders the effective triage model for display ("(runner default)" when
// unset, so the reader knows which model produced the preview).
func modelLabel(cfg store.Config) string {
	if cfg.TriageModel == "" {
		return "runner default"
	}
	return cfg.TriageModel
}

func lensTryRenderHuman(ctx context.Context, st *store.Store, cfg store.Config, runFn distill.MineFunc, ln *lens.Lens, candidate bool, sessions []string) error {
	name := ln.Name
	if candidate {
		name += dim(" (candidate — file's name was reserved)")
	}
	fmt.Printf("%s %s   %s %s\n", label("lens"), bold(name), dim("model:"), modelLabel(cfg))
	fmt.Printf("%s previewing %d session(s), read-only — nothing is written\n\n", label("try"), len(sessions))

	total, driftedAny := 0, false
	for _, sess := range sessions {
		turns := st.RawCount(sess)
		bytes := st.RawBytes(sess)
		fmt.Printf("%s %s  %s\n", cyan("── session"), sess,
			dim(fmt.Sprintf("(%d turns, %d chars) ──", turns, bytes)))

		start := time.Now()
		obs, chunks, drifted, err := distill.PreviewMine(ctx, runFn, cfg, st, sess, ln)
		elapsed := time.Since(start)
		if err != nil {
			// A transport error on one session shouldn't abort the whole preview — report
			// and move on, so a rate-limit on session 2 still lets 1 and 3 render.
			fmt.Printf("   %s %s\n\n", badGlyph(), red("mine failed: "+err.Error()))
			continue
		}

		if chunks > 1 {
			fmt.Printf("   %s %s\n", warnGlyph(), yellow(fmt.Sprintf(
				"session rendered to %d chunks — arc-spanning lenses may fragment across chunk boundaries", chunks)))
		}
		if drifted {
			driftedAny = true
			fmt.Printf("   %s %s\n", warnGlyph(), yellow(
				"model returned no observation array (prose drift) — the triage model may be too weak for this prompt; "+
					"raise it with `witness config set triage_model <stronger>`"))
		}
		if len(obs) == 0 && !drifted {
			fmt.Printf("   %s\n", dim("(no observations — a quiet session for this lens, or the prompt found nothing)"))
		}
		for _, o := range obs {
			total++
			fmt.Printf("   %s %s\n", dim(fmt.Sprintf("[%s p%d]", o.Dimension, o.Poignancy)), o.Observation)
			if o.Evidence != "" {
				fmt.Printf("       %s\n", dim("↳ "+o.Evidence))
			}
		}
		fmt.Printf("   %s\n\n", dim(fmt.Sprintf("%d obs in %.1fs", len(obs), elapsed.Seconds())))
	}

	summary := fmt.Sprintf("%d observation(s) across %d session(s)", total, len(sessions))
	if driftedAny {
		summary += " — some sessions drifted (see above)"
	}
	fmt.Printf("%s %s\n", label("total"), summary)
	return nil
}

func lensTryEmitJSON(ctx context.Context, st *store.Store, cfg store.Config, runFn distill.MineFunc, ln *lens.Lens, candidate bool, sessions []string) error {
	out := lensTryJSON{Lens: ln.Name, Model: modelLabel(cfg), Candidate: candidate}
	for _, sess := range sessions {
		start := time.Now()
		obs, chunks, drifted, err := distill.PreviewMine(ctx, runFn, cfg, st, sess, ln)
		elapsed := time.Since(start)
		if err != nil {
			// Surface the error for this session but keep the array well-formed for diffing.
			return fmt.Errorf("preview session %s: %w", sess, err)
		}
		sj := lensTrySessionJSON{
			Session:      sess,
			RawTurns:     st.RawCount(sess),
			RawBytes:     st.RawBytes(sess),
			ChunkCount:   chunks,
			Drifted:      drifted,
			ElapsedMS:    elapsed.Milliseconds(),
			Observations: []lensTryObsJSON{},
		}
		for _, o := range obs {
			sj.Observations = append(sj.Observations, lensTryObsJSON{
				Dimension:   o.Dimension,
				Observation: o.Observation,
				Evidence:    o.Evidence,
				Poignancy:   o.Poignancy,
			})
			out.TotalObs++
		}
		if drifted {
			out.DriftedAny = true
		}
		out.Sessions = append(out.Sessions, sj)
	}
	return emitJSON(out)
}
