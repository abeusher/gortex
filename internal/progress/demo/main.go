// Command demo drives the live progress renderer through the flagship
// sequences so the animation can be eyeballed (and its byte stream captured)
// without touching a real daemon or repo:
//
//	go run ./internal/progress/demo            # full showcase
//	go run ./internal/progress/demo -scene=track|enrich|fail
//	go run ./internal/progress/demo -plain     # the --no-progress rendering
//
// It is a development aid, deliberately outside cmd/gortex so it never ships
// in the binary.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/zzet/gortex/internal/progress"
)

type phase struct {
	run    string
	done   string
	target int64
	unit   string
	note   string
	dur    time.Duration
}

// The indexing storyboard: scan → parse → graph → cross-repo → communities.
var phases = []phase{
	{"scanning workspace", "files discovered", 2431, "files", "", 900 * time.Millisecond},
	{"parsing sources", "symbols extracted", 48210, "symbols", "go · ts · py · sql · yaml", 1500 * time.Millisecond},
	{"building knowledge graph", "edges materialized", 612940, "edges", "", 1700 * time.Millisecond},
	{"resolving cross-repo edges", "cross-repo links", 1284, "xrepo", "", 1100 * time.Millisecond},
	{"detecting communities", "modules clustered", 7, "modules", "", 800 * time.Millisecond},
}

func main() {
	scene := flag.String("scene", "all", "track|enrich|fail|all")
	plain := flag.Bool("plain", false, "render the --no-progress plain output")
	flag.Parse()

	switch *scene {
	case "track":
		track(*plain)
	case "enrich":
		enrich(*plain)
	case "fail":
		fail(*plain)
	default:
		track(*plain)
		fmt.Fprintln(os.Stderr)
		enrich(*plain)
		fmt.Fprintln(os.Stderr)
		fail(*plain)
	}
}

func newTracker(plain bool, opts ...progress.TrackerOption) *progress.Tracker {
	if plain {
		opts = append(opts, progress.WithoutAnimation())
	}
	return progress.NewTracker(os.Stderr, opts...)
}

// track is the banner-preset showcase: planned phases, counts easing up,
// the mark lighting dot by dot, then the ready line.
func track(plain bool) {
	tr := newTracker(plain, progress.WithLogo())
	tr.Start("gortex track .")
	steps := make([]*progress.Step, len(phases))
	for i, p := range phases {
		steps[i] = tr.AddStep(p.run)
		steps[i].SetUnit(p.unit)
	}
	for i, p := range phases {
		tr.StartStep(p.run)
		if p.note != "" {
			steps[i].Note(p.note)
		}
		countUp(steps[i], p.target, p.dur)
		steps[i].DoneAs(p.done)
	}
	tr.SetStatus("")
	tr.Done("ready", "indexed 2,431 files · 48,210 symbols · 1 repo tracked")
}

// enrich is the compact preset driven purely through the Reporter interface —
// what every existing spinner call site gets for free.
func enrich(plain bool) {
	tr := newTracker(plain)
	tr.Start("gortex enrich all")
	stages := []struct {
		name  string
		total int
		dur   time.Duration
	}{
		{"stamping blame", 420, 700 * time.Millisecond},
		{"loading coverage", 96, 400 * time.Millisecond},
		{"linking releases", 31, 300 * time.Millisecond},
	}
	for _, s := range stages {
		start := time.Now()
		for time.Since(start) < s.dur {
			frac := float64(time.Since(start)) / float64(s.dur)
			tr.Report(s.name, int(frac*float64(s.total)), s.total)
			time.Sleep(30 * time.Millisecond)
		}
		tr.Report(s.name, s.total, s.total)
	}
	tr.Done("", "3 enrichment passes complete")
}

// fail shows the failure state, including a log line above the live region.
func fail(plain bool) {
	tr := newTracker(plain)
	tr.Start("gortex enrich coverage")
	s := tr.StartStep("parsing profile")
	countUp(s, 1200, 500*time.Millisecond)
	s.Done()
	tr.Logf("warning: 3 segments referenced files outside the repo")
	tr.StartStep("stamping nodes")
	time.Sleep(600 * time.Millisecond)
	tr.Fail(errors.New("daemon connection lost"))
}

// countUp eases a counter to target over dur with the design's cubic
// ease-out, ticking faster than the frame rate so the animation stays smooth.
func countUp(s *progress.Step, target int64, dur time.Duration) {
	start := time.Now()
	for {
		t := float64(time.Since(start)) / float64(dur)
		if t >= 1 {
			break
		}
		u := 1 - t
		eased := 1 - u*u*u
		s.Progress(int64(eased*float64(target)), target)
		time.Sleep(25 * time.Millisecond)
	}
	s.Progress(target, target)
}
