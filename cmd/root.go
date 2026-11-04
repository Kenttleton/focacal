package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/Kenttleton/focacal/internal/config"
)

// cfgFlag is the raw --config value: a config directory, or a legacy
// config.yaml path (its directory is used).
var cfgFlag string

// rootCmd is the base command for the focacal CLI.
var rootCmd = &cobra.Command{
	Use:   "focacal",
	Short: "focacal — AI-powered calendar focus scheduler",
	Long: `focacal aggregates your Google and Outlook calendars, uses a local AI model
to plan focus blocks and lunch, and serves the result as a CalDAV feed and REST API.

Run 'focacal start' to launch all services.
Run 'focacal auth google <source-id>' to authorize a Google Calendar source.
Run 'focacal auth outlook <source-id>' to authorize an Outlook source.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFlag, "config", "",
		"config directory, or a legacy config.yaml path (default: search /data/config, $HOME/.config/focacal, .)")
	rootCmd.PersistentFlags().Bool("debug", false, "enable debug logging")
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
}

// loadConfig resolves the config directory, migrates a legacy single-file
// config.yaml in it, and loads it. started is env-free (for
// config.NewService's pending_restart); running carries env overrides and is
// what the process uses. See config.LoadStart.
func loadConfig(log *slog.Logger) (dir string, started, running *config.Config, err error) {
	dir, how := config.ResolveDir(cfgFlag)
	switch how {
	case config.DirLegacy:
		// Refuse rather than start from defaults (which would reopen
		// /auth/setup) or migrate into the data volume (which would split the
		// config between the host file and the volume).
		return "", nil, nil, fmt.Errorf("found a legacy single-file config at %s/config.yaml: "+
			"move config.yaml into ./config/ on the host and mount ./config:/data/config "+
			"(see docs/config-domains.md); it is migrated on the next start", dir)
	case config.DirFlag:
		// A mistyped --config must not become a fresh instance.
		if err := config.CheckFlagDir(dir); err != nil {
			return "", nil, nil, err
		}
	}
	if !config.HasConfigFiles(dir) {
		log.Warn("no config files in the config directory; starting from defaults with first-run /auth/setup open",
			"dir", dir, "how", how)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", nil, nil, fmt.Errorf("config dir %s: %w", dir, err)
	}
	// Migration runs before anything reads the directory.
	migrated, err := config.MigrateLegacy(dir)
	if err != nil {
		return "", nil, nil, err
	}
	if migrated {
		log.Info("migrated legacy config.yaml to the directory layout; original kept as config.yaml.bak",
			"dir", dir)
	}
	if keys := config.LegacyKeys(dir); len(keys) > 0 {
		log.Warn("config.yaml holds keys that now live in planner.yaml / calendars.yaml and are ignored",
			"dir", dir, "keys", keys)
	}
	started, running, err = config.LoadStart(dir)
	if err != nil {
		return "", nil, nil, fmt.Errorf("load config from %s: %w", dir, err)
	}
	// R14: an unset ics_url_env is a warning; only that calendar fails (at
	// sync time). The value is never logged, only the variable name.
	for _, ref := range running.UnresolvedEnvRefs() {
		log.Warn("ics_url_env is not set; this calendar will not sync until it is (add it to .env and restart)",
			"kind", ref.Kind, "id", ref.ID, "env", ref.Env)
	}
	return dir, started, running, nil
}
