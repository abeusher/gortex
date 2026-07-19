package main

import (
	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/progress"
)

// newCLISpinner constructs a Spinner (the compact face of the live Tracker)
// bound to cmd's stderr and starts it. The caller is responsible for
// Done()/Fail(); when the global --no-progress flag is set the spinner falls
// back to plain text. Reporter stage transitions render as live checklist
// rows either way.
func newCLISpinner(cmd *cobra.Command, label string) *progress.Spinner {
	sp := progress.NewSpinner(cmd.ErrOrStderr())
	if noProgress {
		sp.Disable()
	}
	sp.Start(label)
	return sp
}
