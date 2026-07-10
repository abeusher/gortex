package main

import (
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zzet/gortex/internal/agents"
)

// installWizardSelectedDashboard mirrors wizardSelectedDashboard but for the
// install command — kept separate so a wizard run for `gortex init` in one
// shell doesn't influence a subsequent `gortex install` in the same process
// (which never happens in real usage, but matters for tests that exercise
// both wizards in one binary).
var installWizardSelectedDashboard bool

// runInstallWizard launches the same wizard model as `gortex init` but seeds
// it from the install* globals (home-rooted setup vs per-repo) and writes
// picks back into them. The dashboard surface is then driven from runInstall
// via selectInstallProgress.
//
// home is passed in so the underlying Detect calls see the right Env without
// the wizard having to re-derive ${HOME}.
func runInstallWizard(cmd interface {
	ErrOrStderr() io.Writer
}, home string) (bool, error) {
	registry := buildRegistry()
	registered := registry.All()

	env := agents.Env{
		Root:                      mustAbs(installTrackPath),
		Home:                      home,
		Mode:                      agents.ModeGlobal,
		InstallHooks:              installHooks,
		HookMode:                  installHookMode,
		InstallGlobalInstructions: installClaudeMd,
	}
	detected := detectAdapters(registered, env)

	defaults := initDefaults{
		hooks:    installHooks,
		hookMode: firstNonEmpty(installHookMode, "deny"),
		// install doesn't index — leave analyze/skills off in the wizard so
		// the toggles still render but reflect that the global install path
		// won't touch the project.
		analyze: false,
		skills:  false,
		// Machine-global anonymous telemetry — install is the place to choose
		// it. Seeded from the current setting (installTelemetry was primed from
		// the persisted choice in runInstall).
		telemetry:     installTelemetry,
		showTelemetry: true,
	}
	model := newInstallWizardModel(home, registered, detected, defaults)

	prog := tea.NewProgram(model,
		tea.WithOutput(cmd.ErrOrStderr()),
		tea.WithAltScreen(),
		tea.WithoutSignalHandler(),
	)
	finalModel, err := prog.Run()
	if err != nil {
		return false, fmt.Errorf("wizard: %w", err)
	}
	m, ok := finalModel.(*initWizardModel)
	if !ok || m.cancelled || !m.confirmed {
		return true, nil
	}

	// Pour wizard picks back into install* globals.
	installHooks = m.hooks
	installHookMode = m.hookMode
	installTelemetry = m.telemetry
	if len(m.pickedAgents) > 0 {
		installAgents = strings.Join(m.pickedAgents, ",")
	}
	installWizardSelectedDashboard = true
	return false, nil
}

// newInstallWizardModel builds the wizard with install-specific copy on the
// banner title and subtitle. The widget tree is identical to
// newInitWizardModel; we just swap the title to "gortex install" and the
// rootPath to a friendly "your machine" so the subtitle reads sensibly.
func newInstallWizardModel(home string, registered []agents.Adapter, detected map[string]bool, defaults initDefaults) *initWizardModel {
	m := newInitWizardModel(home, registered, detected, defaults)
	m.title = "gortex install"
	m.rootPath = "your machine"
	return m
}

// selectInstallProgress returns the right initProgress surface for the
// install run. Same precedence as init: --no-progress → plain line tracker;
// wizard ran → dashboard; otherwise the banner tracker.
func selectInstallProgress(w io.Writer) initProgress {
	stages := []string{stageAdapters}
	if noProgress {
		return newTrackerProgress(w, stages, true)
	}
	if installWizardSelectedDashboard {
		// Install has exactly one high-level stage (adapters); the dashboard
		// is still valuable because it shows the per-adapter sub-status as a
		// framed live view instead of a single line that scrolls past.
		if s := startInitDashboard(w, stages); s != nil {
			return newDashboardProgress(s)
		}
	}
	return newTrackerProgress(w, stages, false)
}
