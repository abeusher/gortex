package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/skills"
)

var (
	skillsOutputDir      string
	skillsMinSize        int
	skillsMaxCommunities int
	skillsUpdateClaudeMD bool
	skillsIndex          string
)

var skillsCmd = &cobra.Command{
	Use:   "skills [path]",
	Short: "Generate per-community skill files from graph analysis",
	Long:  "Indexes a repository, detects communities, and generates SKILL.md files for each significant community. Skills are placed in .claude/skills/generated/ for Claude Code auto-discovery.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSkills,
}

func init() {
	skillsCmd.Flags().StringVar(&skillsOutputDir, "output-dir", "", "output directory (default .claude/skills/generated/)")
	skillsCmd.Flags().IntVar(&skillsMinSize, "min-size", 3, "minimum community size to generate a skill")
	skillsCmd.Flags().IntVar(&skillsMaxCommunities, "max-communities", 20, "maximum number of skills to generate")
	skillsCmd.Flags().BoolVar(&skillsUpdateClaudeMD, "update-claude-md", true, "update CLAUDE.md with routing table")
	skillsCmd.Flags().StringVar(&skillsIndex, "index", "", "repository path to index (default: current directory)")
	rootCmd.AddCommand(skillsCmd)
}

func runSkills(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	// Determine repo path.
	repoPath := "."
	if len(args) > 0 {
		repoPath = args[0]
	}
	if skillsIndex != "" {
		repoPath = skillsIndex
	}

	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	// Output directory.
	outputDir := skillsOutputDir
	if outputDir == "" {
		outputDir = filepath.Join(absPath, ".claude", "skills", "generated")
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Index the repository.
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, cfg.Index, logger)

	fmt.Fprintf(os.Stderr, "[gortex] skills: indexing %s...\n", absPath)
	result, err := idx.Index(absPath)
	if err != nil {
		return fmt.Errorf("indexing: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[gortex] skills: indexed %d files (%d nodes, %d edges) in %dms\n",
		result.FileCount, result.NodeCount, result.EdgeCount, result.DurationMs)

	// Run analysis.
	fmt.Fprintf(os.Stderr, "[gortex] skills: detecting communities...\n")
	communities := analysis.DetectCommunities(g)
	processes := analysis.DiscoverProcesses(g)

	if len(communities.Communities) == 0 {
		fmt.Fprintf(os.Stderr, "[gortex] skills: no communities detected\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "[gortex] skills: found %d communities (modularity: %.2f)\n",
		len(communities.Communities), communities.Modularity)

	// Generate skills.
	gen := skills.New(communities, processes, g)
	gen.SetMinSize(skillsMinSize)
	gen.SetMaxSkills(skillsMaxCommunities)

	generated := gen.GenerateAll()
	if len(generated) == 0 {
		fmt.Fprintf(os.Stderr, "[gortex] skills: no communities large enough (min-size: %d)\n", skillsMinSize)
		return nil
	}

	// Clean output directory.
	_ = os.RemoveAll(outputDir)

	// Write skill files.
	for _, s := range generated {
		dir := filepath.Join(outputDir, s.DirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating skill dir %s: %w", dir, err)
		}
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte(s.Content), 0o644); err != nil {
			return fmt.Errorf("writing skill %s: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "[gortex] skills: wrote %s (%d symbols)\n", s.DirName, gen.CommunitySize(s.CommunityID))
	}

	// Update CLAUDE.md with routing table.
	if skillsUpdateClaudeMD {
		claudeMDPath := filepath.Join(absPath, "CLAUDE.md")
		routing := gen.GenerateRouting(generated)
		if err := updateClaudeMDSkills(claudeMDPath, routing); err != nil {
			fmt.Fprintf(os.Stderr, "[gortex] skills: warning: could not update CLAUDE.md: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[gortex] skills: updated CLAUDE.md with %d community skills\n", len(generated))
		}
	}

	fmt.Fprintf(os.Stderr, "[gortex] skills: generated %d skills in %s\n", len(generated), outputDir)
	return nil
}

const (
	skillsStartMarker = "<!-- gortex:skills:start -->"
	skillsEndMarker   = "<!-- gortex:skills:end -->"
)

// updateClaudeMDSkills inserts or replaces the skills routing table in CLAUDE.md.
func updateClaudeMDSkills(path, routing string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create CLAUDE.md with just the routing table.
			return os.WriteFile(path, []byte(routing), 0o644)
		}
		return err
	}

	text := string(content)

	startIdx := strings.Index(text, skillsStartMarker)
	endIdx := strings.Index(text, skillsEndMarker)

	if startIdx >= 0 && endIdx >= 0 {
		// Replace existing block.
		text = text[:startIdx] + routing + text[endIdx+len(skillsEndMarker):]
	} else {
		// Append to end.
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n" + routing
	}

	return os.WriteFile(path, []byte(text), 0o644)
}
